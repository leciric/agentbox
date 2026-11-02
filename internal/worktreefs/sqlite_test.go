package worktreefs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentbox/internal/testutil"
)

// sampleDB writes a database at root/rel: a table "people" of n rows with an
// integer too large for JavaScript, a blob and a null, a table whose name
// needs quoting, and a view.
func sampleDB(t *testing.T, root, rel string, n int) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, q := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT, big INTEGER, photo BLOB, note TEXT)`,
		`CREATE TABLE "odd ""name""" (x)`,
		`CREATE VIEW adults AS SELECT name FROM people`,
		`INSERT INTO "odd ""name""" VALUES (1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= n; i++ {
		if _, err := db.Exec(`INSERT INTO people (name, big, photo, note) VALUES (?, ?, ?, NULL)`, "p"+string(rune('a'+i%26)), int64(1)<<60, []byte{0, 1, 2}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLiteListsTablesAndPagesRows(t *testing.T) {
	root := t.TempDir()
	sampleDB(t, root, "data/app.db", 30)
	ctx := context.Background()

	got, err := SQLite(ctx, root, "data/app.db", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tb := range got.Tables {
		names = append(names, tb.Name)
	}
	if strings.Join(names, ",") != `adults,odd "name",people` || !got.Tables[0].View || got.Tables[2].View {
		t.Fatalf("tables = %+v", got.Tables)
	}
	if got.Table != "" || len(got.Rows) != 0 {
		t.Fatalf("no table asked, got %+v", got)
	}

	page, err := SQLite(ctx, root, "data/app.db", "people", 25, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 30 || page.Offset != 25 || len(page.Rows) != 5 {
		t.Fatalf("total %d offset %d rows %d", page.Total, page.Offset, len(page.Rows))
	}
	if strings.Join(page.Columns, ",") != "id,name,big,photo,note" {
		t.Fatalf("columns = %v", page.Columns)
	}
	row := page.Rows[0]
	if row[0] != int64(26) || row[2] != "1152921504606846976" || row[4] != nil {
		t.Fatalf("row = %#v", row)
	}
	if b, ok := row[3].(map[string]int); !ok || b["blob"] != 3 {
		t.Fatalf("blob cell = %#v", row[3])
	}

	odd, err := SQLite(ctx, root, "data/app.db", `odd "name"`, 0, 0)
	if err != nil || len(odd.Rows) != 1 || odd.Total != 1 {
		t.Fatalf("quoted table: %+v, %v", odd, err)
	}
	if _, err := SQLite(ctx, root, "data/app.db", "people; DROP TABLE people", 0, 10); !errors.Is(err, ErrNoTable) {
		t.Fatalf("unknown table: %v", err)
	}
}

func TestSQLiteIsReadOnlyAndRefusesOtherFiles(t *testing.T) {
	root := t.TempDir()
	sampleDB(t, root, "plain.db", 1)
	if err := os.Rename(filepath.Join(root, "plain.db"), filepath.Join(root, "a?b#c.db")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(root, "a?b#c.db"))
	if _, err := SQLite(context.Background(), root, "a?b#c.db", "people", 0, 10); err != nil {
		t.Fatalf("a path with ? and #: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "a?b#c.db"))
	if string(before) != string(after) {
		t.Fatal("reading changed the database")
	}

	write(t, root, "notes.txt", "hello")
	if _, err := SQLite(context.Background(), root, "notes.txt", "", 0, 0); err == nil || !strings.Contains(err.Error(), "not an SQLite database") {
		t.Fatalf("a text file: %v", err)
	}
	if _, err := SQLite(context.Background(), root, "../x.db", "", 0, 0); !errors.Is(err, ErrOutside) {
		t.Fatalf("outside: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "notes.txt-journal")); err == nil {
		t.Fatal("a journal was made")
	}
}

func TestReadMarksPDFsAndDatabases(t *testing.T) {
	root := t.TempDir()
	write(t, root, "doc.PDF", "%PDF-1.4\n...")
	sampleDB(t, root, "app.sqlite3", 1)

	pdf, err := Read(root, "doc.PDF", false)
	if err != nil || !pdf.PDF || pdf.Data == "" || pdf.Text != "" || pdf.Binary {
		t.Fatalf("pdf = %+v, %v", pdf, err)
	}
	db, err := Read(root, "app.sqlite3", false)
	if err != nil || !db.Binary || !db.SQLite {
		t.Fatalf("database = %+v, %v", db, err)
	}

	big, err := os.Create(filepath.Join(root, "big.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(PDFLimit + 1); err != nil {
		t.Fatal(err)
	}
	_ = big.Close()
	if c, err := Read(root, "big.pdf", false); err != nil || !c.PDF || !c.TooLarge || c.Data != "" {
		t.Fatalf("big pdf = %+v, %v", c.TooLarge, err)
	}
}

func TestSQLiteAtACommit(t *testing.T) {
	root := t.TempDir()
	testutil.Git(t, root, "init", "-q", "-b", "main")
	sampleDB(t, root, "app.db", 3)
	testutil.Git(t, root, "add", "-A")
	testutil.Git(t, root, "commit", "-q", "-m", "db")
	commit := strings.TrimSpace(testutil.Git(t, root, "rev-parse", "HEAD"))
	if err := os.Remove(filepath.Join(root, "app.db")); err != nil {
		t.Fatal(err)
	}

	got, err := SQLiteAt(context.Background(), root, commit, "app.db", "people", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || len(got.Rows) != 2 || len(got.Tables) != 3 {
		t.Fatalf("got %+v", got)
	}
}
