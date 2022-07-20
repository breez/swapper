package submarineswap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/btcsuite/btcd/btcec"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
)

var (
	pgxPool *pgxpool.Pool
)

func pgConnect() error {
	var err error
	pgxPool, err = pgxpool.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("pgxpool.Connect(%v): %w", os.Getenv("DATABASE_URL"), err)
	}
	return nil
}
func saveSwapperSubmarineData(netID byte, hash []byte, lockHeight int64, swapperKey []byte, script []byte) error {

	if len(swapperKey) != btcec.PrivKeyBytesLen {
		return errors.New("swapperKey not valid")
	}

	commandTag, err := pgxPool.Exec(context.Background(),
		`INSERT INTO
	submarineswap (netID, hash, lockHeight, swapperKey,script)
	VALUES ($1, $2, $3, $4, $5)
	ON CONFLICT DO NOTHING`,
		netID, hash, lockHeight, swapperKey, script)
	log.Printf("submarineswap(%x, %x, %v, %x,%x) rows: %v err: %v",
		netID, hash, lockHeight, swapperKey, script, commandTag.RowsAffected(), err)
	if err != nil {
		return fmt.Errorf("registerPayment(%x, %x, %v, %x, %x) error: %w",
			netID, hash, lockHeight, swapperKey, script, err)
	}

	return nil
}

func getSwapperSubmarineData(hash []byte) (lockHeight int64, swapperKey, script []byte, err error) {

	var netID byte

	err = pgxPool.QueryRow(context.Background(),
		`SELECT netID, hash, lockHeight, swapperKey,script
			FROM submarineswap
			WHERE hash=$1 OR sha256('probing-01:' || hash)=$1`,
		hash).Scan(&netID, &hash, &lockHeight, &swapperKey, &script)
	if err != nil {
		if err == pgx.ErrNoRows {
			err = nil
		}
		return 0, nil, nil, err
	}

	return 0, nil, nil, nil
}
