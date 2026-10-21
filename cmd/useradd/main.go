// Command useradd creates a doctor's account in the API's database.
//
//	go run ./cmd/useradd -user ivanova -password secret
//
// It reads DATA_DIR / DB_PATH like the API, and is safe to run while the API
// is up: SQLite waits for the other writer.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nikenesx/aura-server/internal/auth"
	"github.com/nikenesx/aura-server/internal/store"
)

func main() {
	user := flag.String("user", "", "login")
	password := flag.String("password", "", "password")
	flag.Parse()
	if err := run(*user, *password); err != nil {
		fmt.Fprintln(os.Stderr, "useradd:", err)
		os.Exit(1)
	}
}

func run(user, password string) error {
	path := os.Getenv("DB_PATH")
	if path == "" {
		dir := os.Getenv("DATA_DIR")
		if dir == "" {
			dir = "data"
		}
		path = filepath.Join(dir, "aura.db")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer db.Close()

	svc, err := auth.NewService(db, time.Hour)
	if err != nil {
		return err
	}
	u, err := svc.CreateUser(ctx, user, password)
	if err != nil {
		return fmt.Errorf("create %q: %w", user, err)
	}
	fmt.Printf("created %s (%s) in %s\n", u.Username, u.ID, path)
	return nil
}
