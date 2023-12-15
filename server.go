package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Roasbeef/btcutil"
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
	DEV_FRONT_URL  = os.Getenv("DEV_FRONT_URL")
	PROD_FRONT_URL = os.Getenv("PROD_FRONT_URL")
	ALLOW_LIST     = []string{"http://localhost:5173", "http://localhost:8080", `https://\S*-studioteatwo.vercel.app`, DEV_FRONT_URL, PROD_FRONT_URL}

	// Lightning node
	LNC_PASSPRASE = os.Getenv("LNC_PASSPRASE")
	LNC_MAILBOX   = os.Getenv("LNC_MAILBOX")

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
	// TODO: goroutine

	// put at first.
	interceptor, err := signal.Intercept()
	if err != nil {
		log.Critical(err)
		os.Exit(1)
	}

	// set logs
	SetupLoggers(logWriter, interceptor)
	log.Info(appDataDir)
	logFile := filepath.Join(appDataDir, defaultLogFilename)
	err = logWriter.InitLogRotator(
		logFile, defaultMaxLogFileSize, defaultMaxLogFiles,
	)
	if err != nil {
		log.Critical(err)
		os.Exit(1)
	}
	err = build.ParseAndSetDebugLevels(defaultLogLevel, logWriter)
	if err != nil {
		log.Critical(err)
		os.Exit(1)
	}

	// Connect to LNC
	errChan := make(chan error)
	mint, err := setup(errChan)
	if err != nil {
		log.Critical(err)
		os.Exit(1)
	}
	nch := &NewChallengeHandler{mint}
	vh := &VerifyHandler{mint}

	// Set up server
	router := http.NewServeMux()
	router.Handle("/createInvoice", nch)
	router.Handle("/verify", vh)

	c := cors.New(cors.Options{
		AllowedOrigins:   ALLOW_LIST,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions},
		AllowedHeaders:   []string{"*"},
		AllowCredentials: true,
	})
	handler := c.Handler(router)

	log.Info("start server")
	http.ListenAndServe(":8180", handler)

	select {
	case <-interceptor.ShutdownChannel():
		log.Critical("Received interrupt signal, shutting down aperture.")
		os.Exit(1)
	case err := <-errChan:
		log.Critical("Error while running aperture: %v", err)
		os.Exit(1)
	}
}

func setup(errChan chan error) (*mint.Mint, error) {
	fileInfo, err := os.Lstat(appDataDir)
	if err != nil {
		fileMode := fileInfo.Mode()
		unixPerms := fileMode & os.ModePerm
		if err := os.Mkdir(appDataDir, unixPerms); err != nil {
			return nil, fmt.Errorf("unable to create directory "+
				"mkdir: %w", err)
		}
	}

	dbCfg := aperturedb.SqliteConfig{SkipMigrations: false, DatabaseFileName: appDataDir + "/" + defaultSqliteDatabaseFileName}
	db, err := aperturedb.NewSqliteStore(&dbCfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create store "+
			"db: %w", err)
	}
	dbSecretTxer := aperturedb.NewTransactionExecutor(db,
		func(tx *sql.Tx) aperturedb.SecretsDB {
			return db.WithTx(tx)
		},
	)
	secretStore := aperturedb.NewSecretsStore(dbSecretTxer)
	dbLNCTxer := aperturedb.NewTransactionExecutor(db,
		func(tx *sql.Tx) aperturedb.LNCSessionsDB {
			return db.WithTx(tx)
		},
	)
	lncStore := aperturedb.NewLNCSessionsStore(dbLNCTxer)

	session, err := lnc.NewSession(
		LNC_PASSPRASE,
		LNC_MAILBOX,
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create lnc "+
			"session: %w", err)
	}

	genInvoiceReq := func(price int64, params *nostr.NostrPublishParam) (*lnrpc.Invoice, error) {
		return &lnrpc.Invoice{
			Value:  price,
			Expiry: 2592000, // 30 days
		}, nil
	}

	nostrClient, err := nostr.NewNostrClient(N_SEC_KEY, SERVICE_NAME, relayList)
	if err != nil {
		return nil, fmt.Errorf("unable to create nostr client: %w", err)
	}

	log.Info("LNC challlenge strat")
	challenger, err := challenger.NewLNCChallenger(
		session, lncStore, genInvoiceReq, nostrClient, errChan,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to start lnc "+
			"challenger: %w", err)
	}
	log.Info("LNC challlenge succeeded ", challenger)

	mint := mint.New(&mint.Config{
		Secrets:    secretStore,
		Challenger: challenger,
		ServiceLimiter: &mockServiceLimiter{
			capabilities: make(map[lsat.Service]lsat.Caveat),
			constraints:  make(map[lsat.Service][]lsat.Caveat),
			timeouts:     make(map[lsat.Service]lsat.Caveat),
		},
		Now: time.Now,
	})

	return mint, nil
}
