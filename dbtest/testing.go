// Package dbtest provides utilities to use dockertest with PostgreSQL.
package dbtest

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v4/stdlib"
	"pkg.iterate.no/pgutil"

	"github.com/ory/dockertest/v3"
)

var pool *dockertest.Pool
var poolCfg *dbCfg

type dbCfg struct {
	init       [][]byte
	image, tag string
}

type DBConfigFn func(c *dbCfg)

// WithPool makes sure we have a valid database pool. You should wrap your TestMain invocation with this.
//
//    func TestMain(m *testing.M) {
//        os.Exit(withPool(m.Run))
//    }
func WithPool(f func() int, opts ...DBConfigFn) int {
	var p *dockertest.Pool
	if poolCfg == nil {
		poolCfg = &dbCfg{
			image: "postgres",
			tag:   "13-alpine",
		}
	}
	for i := range opts {
		opts[i](poolCfg)
	}

	log.Println("creating test database")

	p, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("could not connect to docker: %v", err)
	}
	pool = p
	return f()
}

func dbname() string {
	bs := make([]byte, 8)
	if _, err := rand.Read(bs); err != nil {
		log.Fatalln("could not generate db name")
	}
	return fmt.Sprintf("%x", bs)
}

type TDB struct {
	*testing.T
	DB *sql.DB
}

// RunWithDB creates a new database for a subtest.
func RunWithDB(t *testing.T, name string, f func(*TDB)) {
	t.Run(name, func(t *testing.T) {
		WithDB(t, f)
	})
}

// WithDB is like RunWithDB except it doesn't start a new sub-test.
//
//    func TestFunction(t *testing.T) {
//        dbtest.WithDB(t, func(t) {
//            // do stuff here
//        })
//    }
func WithDB(t *testing.T, f func(*TDB)) {
	n := dbname()
	t.Logf("creating database %s", n)
	if pool == nil {
		t.Fatalf("pool not configured")
	}
	db, r, err := makeDB(t, pool)
	if err != nil {
		t.Errorf("could not create testing database: %v", err)
		return
	}
	defer func() {
		pool.Purge(r)
	}()

	for i := range poolCfg.init {
		var b bytes.Buffer
		b.Write(poolCfg.init[i])
		if _, err := db.Exec(b.String()); err != nil {
			t.Errorf("could not execute init script: %v", err)
		}
	}

	f(&TDB{
		T:  t,
		DB: db,
	})
}

// makeDb creates a temporary database.
func makeDB(t testing.TB, p *dockertest.Pool) (*sql.DB, *dockertest.Resource, error) {
	pwd := "pgtest"
	dbn := dbname()

	vars := []string{
		"POSTGRES_USER=dockertest",
		fmt.Sprintf("POSTGRES_DB=%s", dbn),
		fmt.Sprintf("POSTGRES_PASSWORD=%s", pwd),
	}

	r, err := p.Run(poolCfg.image, poolCfg.tag, vars)
	if err != nil {
		return nil, nil, fmt.Errorf("could not start resource: %v", err)
	}
	port := r.GetPort("5432/tcp")

	db, err := sql.Open(
		"pgx",
		fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", "localhost", port, "dockertest", pwd, dbn),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to database: %w", err)
	}
	ctx, ccl := context.WithTimeout(context.Background(), time.Minute*5)
	defer ccl()

	if err := pgutil.Wait(ctx, db); err != nil {
		return nil, nil, ctx.Err()
	}

	return db, r, nil
}

func WithImage(img string) DBConfigFn {
	return func(c *dbCfg) {
		ps := strings.Split(img, ":")
		switch len(ps) {
		case 1:
			c.image = ps[0]
			c.tag = "latest"
		case 2:
			c.image = ps[0]
			c.tag = ps[1]
		default:
			panic(fmt.Sprintf("invalid format, must be %q or %q", "image", "image:tag"))
		}
	}
}

func WithInit(b []byte) DBConfigFn {
	return func(c *dbCfg) {
		c.init = append(c.init, b)
	}
}
