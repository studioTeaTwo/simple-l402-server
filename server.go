package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/lightninglabs/aperture/aperturedb"
	"github.com/lightninglabs/aperture/challenger"
	"github.com/lightninglabs/aperture/lnc"
	"github.com/lightninglabs/aperture/lsat"
	"github.com/lightninglabs/aperture/mint"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/roasbeef/btcutil"
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

	ALLOW_LIST = []string{"http://localhost:5173", "http://localhost:8080", "https://*-studioteatwo.vercel.app", DEV_FRONT_URL, PROD_FRONT_URL}
)

func main() {
	if LNC_PASSPRASE == "" || LNC_MAILBOX == "" {
		log.Fatal("not enough to ENV:", LNC_PASSPRASE, LNC_MAILBOX)
	}

	mint, err := connectLnc()
	if err != nil {
		log.Fatal(err)
	}
	nch := &NewChallengeHandler{mint}
	vh := &VerifyHandler{mint}

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

	log.Println("start server")
	log.Fatal(http.ListenAndServe(":8180", handler))
}

func connectLnc() (*mint.Mint, error) {
	errChan := make(chan error)

	appDataDir := btcutil.AppDataDir("aperture", false)
	fmt.Println(appDataDir)

	fileInfo, err := os.Lstat(appDataDir)
	if err != nil {
		fileMode := fileInfo.Mode()
		unixPerms := fileMode & os.ModePerm
		if err := os.Mkdir(appDataDir, unixPerms); err != nil {
			return nil, fmt.Errorf("unable to create directory "+
				"mkdir: %w", err)
		}
	}

	dbCfg := aperturedb.SqliteConfig{SkipMigrations: false, DatabaseFileName: appDataDir + "/aperture.db"}
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

	log.Println("LNC challlenge strat")
	challenger, err := challenger.NewLNCChallenger(
		session, lncStore, genInvoiceReq, errChan,
	)
	if err != nil {
		return nil, fmt.Errorf("unable to start lnc "+
			"challenger: %w", err)
	}
	log.Println("LNC challlenge succeeded ", challenger)

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
