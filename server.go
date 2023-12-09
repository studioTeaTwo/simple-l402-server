package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Roasbeef/btcutil"
	"github.com/lightninglabs/aperture/aperturedb"
	"github.com/lightninglabs/aperture/challenger"
	"github.com/lightninglabs/aperture/lnc"
	"github.com/lightninglabs/aperture/lsat"
	"github.com/lightninglabs/aperture/mint"
	"github.com/lightningnetwork/lnd/build"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/signal"
	"github.com/rs/cors"
)

const (
	SERVICE_NAME = "SelfSovereignBlog"
)

var (
	DEV_FRONT_URL  = os.Getenv("DEV_FRONT_URL")
	PROD_FRONT_URL = os.Getenv("PROD_FRONT_URL")

	LNC_PASSPRASE = os.Getenv("LNC_PASSPRASE")
	LNC_MAILBOX   = os.Getenv("LNC_MAILBOX")

	ALLOW_LIST = []string{"http://localhost:5173", "http://localhost:8080", `https://\S*-studioteatwo.vercel.app`, DEV_FRONT_URL, PROD_FRONT_URL}

	appDataDir                    = btcutil.AppDataDir("l402", false)
	defaultLogLevel               = "debug"
	defaultLogFilename            = "l402.log"
	defaultMaxLogFiles            = 3
	defaultMaxLogFileSize         = 10
	defaultSqliteDatabaseFileName = "l402.db"
)

func main() {
	if LNC_PASSPRASE == "" || LNC_MAILBOX == "" {
		log.Critical("not enough to ENV:", LNC_PASSPRASE, LNC_MAILBOX)
	}

	// put at first.
	interceptor, err := signal.Intercept()
	if err != nil {
		log.Critical(err)
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
	}
	err = build.ParseAndSetDebugLevels(defaultLogLevel, logWriter)
	if err != nil {
		log.Critical(err)
	}

	// Connect to LNC
	errChan := make(chan error)
	mint, err := connectLnc(errChan)
	if err != nil {
		log.Critical(err)
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
	log.Critical(http.ListenAndServe(":8180", handler))

	select {
	case <-interceptor.ShutdownChannel():
		log.Info("Received interrupt signal, shutting down aperture.")
	case err := <-errChan:
		log.Errorf("Error while running aperture: %v", err)
	}
}

func connectLnc(errChan chan error) (*mint.Mint, error) {
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

	genInvoiceReq := func(price int64) (*lnrpc.Invoice, error) {
		return &lnrpc.Invoice{
			Memo:  SERVICE_NAME,
			Value: price,
		}, nil
	}

	log.Info("LNC challlenge strat")
	challenger, err := challenger.NewLNCChallenger(
		session, lncStore, genInvoiceReq, errChan,
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
