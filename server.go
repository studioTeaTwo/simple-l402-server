package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/Roasbeef/btcutil"
	"github.com/jessevdk/go-flags"
	"github.com/lightninglabs/lndclient"
	"github.com/lightningnetwork/lnd/build"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/signal"
	"github.com/rs/cors"
	"github.com/studioTeaTwo/aperture/aperturedb"
	"github.com/studioTeaTwo/aperture/challenger"
	"github.com/studioTeaTwo/aperture/lnc"
	"github.com/studioTeaTwo/aperture/lsat"
	"github.com/studioTeaTwo/aperture/mint"
	"github.com/studioTeaTwo/aperture/nostr"
)

const (
	SERVICE_NAME = "SelfSovereignBlog"
)

var (
	// front server
	DEV_FRONT_URL  = strings.Split(os.Getenv("DEV_FRONT_URL"), ",")
	PROD_FRONT_URL = os.Getenv("PROD_FRONT_URL")
	ALLOW_LIST     = append([]string{"http://localhost:5173", "http://localhost:8080", PROD_FRONT_URL}, DEV_FRONT_URL...)

	// Lightning node
	HOW_TO_CONNECT = os.Getenv("HOW_TO_CONNECT")
	LNC_PASSPRASE  = os.Getenv("LNC_PASSPRASE")
	LNC_MAILBOX    = os.Getenv("LNC_MAILBOX")

	// Nostr
	N_SEC_KEY = os.Getenv("N_SEC_KEY")
	// combine with user's relay list later
	relayList = []string{
		"wss://relayable.org",
		"wss://relay.damus.io",
		"wss://relay.snort.social",
		"wss://relay.primal.net",
		"wss://yabu.me",
		"wss://r.kojira.io",
	}

	// app settings
	appDataDir                    = btcutil.AppDataDir("l402", false)
	defaultLogLevel               = "debug"
	defaultLogFilename            = "l402.log"
	defaultMaxLogFiles            = 3
	defaultMaxLogFileSize         = 10
	defaultSqliteDatabaseFileName = "l402.db"
)

func main() {
	err := run()

	// Unwrap our error and check whether help was requested from our flag
	// library. If the error is not wrapped, Unwrap returns nil. It is
	// still safe to check the type of this nil error.
	flagErr, isFlagErr := errors.Unwrap(err).(*flags.Error)
	isHelpErr := isFlagErr && flagErr.Type == flags.ErrHelp

	// If we got a nil error, or help was requested, just exit.
	if err == nil || isHelpErr {
		fmt.Println("shutdown normally")
		os.Exit(0)
	}

	// Print any other non-help related errors.
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func run() error {
	// put at first.
	interceptor, err := signal.Intercept()
	if err != nil {
		return err
	}

	// set logs
	SetupLoggers(logWriter, interceptor)
	log.Info(appDataDir)
	logFile := filepath.Join(appDataDir, defaultLogFilename)
	err = logWriter.InitLogRotator(
		logFile, defaultMaxLogFileSize, defaultMaxLogFiles,
	)
	if err != nil {
		return fmt.Errorf("unable to create log directory %w", err)
	}
	err = build.ParseAndSetDebugLevels(defaultLogLevel, logWriter)
	if err != nil {
		return fmt.Errorf("unable to create log level %w", err)
	}

	// Connect to LNC
	errChan := make(chan error)
	mint, challenger, db, err := setup(errChan)
	if err != nil {
		return fmt.Errorf("unable to connect LNC %w", err)
	}
	nch := &NewChallengeHandler{mint}
	vh := &VerifyHandler{mint}

	// Set up server
	router := http.NewServeMux()
	router.Handle("/newchallenge", nch)
	router.Handle("/verify", vh)
	router.HandleFunc("/healthcheck", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "Hello\n")
	})

	c := cors.New(cors.Options{
		AllowedOrigins:   ALLOW_LIST,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	handler := c.Handler(router)

	log.Info("start server")
	http.ListenAndServe(":8180", handler)

	select {
	case <-interceptor.ShutdownChannel():
		log.Info("received interrupt signal, shutting down aperture.")

	case err := <-errChan:
		log.Errorf("error while running aperture: %#v", err)
	}

	// TODO: clean up goroutine & WaitGroup
	challenger.Stop()
	return db.Close()
}

func setup(errChan chan error) (*mint.Mint, mint.Challenger, *aperturedb.SqliteStore, error) {
	fileInfo, err := os.Lstat(appDataDir)
	if err != nil {
		fileMode := fileInfo.Mode()
		unixPerms := fileMode & os.ModePerm
		if err := os.Mkdir(appDataDir, unixPerms); err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create directory "+
				"mkdir: %w", err)
		}
	}

	dbCfg := aperturedb.SqliteConfig{SkipMigrations: false, DatabaseFileName: appDataDir + "/" + defaultSqliteDatabaseFileName}
	db, err := aperturedb.NewSqliteStore(&dbCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to create store "+
			"db: %w", err)
	}
	dbSecretTxer := aperturedb.NewTransactionExecutor(db,
		func(tx *sql.Tx) aperturedb.SecretsDB {
			return db.WithTx(tx)
		},
	)
	secretStore := aperturedb.NewSecretsStore(dbSecretTxer)

	genInvoiceReq := func(price int64, params *nostr.NostrPublishParam) (*lnrpc.Invoice, error) {
		return &lnrpc.Invoice{
			Value:  price,
			Expiry: 2592000, // 30 days
		}, nil
	}

	nostrClient, err := nostr.NewNostrClient(N_SEC_KEY, SERVICE_NAME, relayList)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to create nostr client: %w", err)
	}

	var (
		lncStore    lnc.Store
		_challenger challenger.Challenger
	)

	if HOW_TO_CONNECT == "lnc" {
		dbLNCTxer := aperturedb.NewTransactionExecutor(db,
			func(tx *sql.Tx) aperturedb.LNCSessionsDB {
				return db.WithTx(tx)
			},
		)
		lncStore = aperturedb.NewLNCSessionsStore(dbLNCTxer)

		session, err := lnc.NewSession(
			LNC_PASSPRASE,
			LNC_MAILBOX,
			false,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create lnc "+
				"session: %w", err)
		}

		log.Info("LNC challlenge strat")
		_challenger, err = challenger.NewLNCChallenger(
			session, lncStore, genInvoiceReq, nostrClient, errChan,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to start lnc "+
				"challenger: %w", err)
		}
		log.Infof("LNC challlenge succeeded %#v", _challenger)
	} else {
		log.Info("Using lnd's authenticator config")

		usr, _ := user.Current()
		client, err := lndclient.NewBasicClient(
			"localhost:10009", usr.HomeDir+"/.lnd/tls.cert",
			usr.HomeDir+"/.lnd/data/chain/bitcoin/mainnet", "mainnet",
			lndclient.MacFilename(
				"invoice.macaroon",
			),
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create lnd client %w", err)
		}

		_challenger, err = challenger.NewLndChallenger(
			client, genInvoiceReq, *nostrClient, context.Background,
			errChan,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to start lnd "+
				"challenger: %w", err)
		}
		log.Infof("LND challlenge succeeded %#v", _challenger)
	}

	mint := mint.New(&mint.Config{
		Secrets:    secretStore,
		Challenger: _challenger,
		ServiceLimiter: &mockServiceLimiter{
			capabilities: make(map[lsat.Service]lsat.Caveat),
			constraints:  make(map[lsat.Service][]lsat.Caveat),
			timeouts:     make(map[lsat.Service]lsat.Caveat),
		},
		Now: time.Now,
	})

	return mint, _challenger, db, nil
}
