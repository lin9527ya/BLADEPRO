package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseGuardRejectsApplicationAndKeywordDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("BLADEPRO_CONFIG", path)
	if err := os.WriteFile(path, []byte(`{"database":{"dsn":"postgres://u:secret@localhost:5432/bladepro_test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dsn := range []string{"postgres://different:secret@127.0.0.1:5432/bladepro_test", "host=::1 port=5432 dbname=bladepro_test user=someone"} {
		if err := guardTestDSN(dsn); err == nil {
			t.Fatal("configured application database accepted")
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatal("password leaked")
		}
	}
	for _, dsn := range []string{"postgres://u@127.0.0.1/bladepro", "host=127.0.0.1 dbname=latest_production user=u"} {
		if err := guardTestDSN(dsn); err == nil {
			t.Fatal("non-test database accepted")
		}
	}
	if err := guardTestDSN("host=127.0.0.1 port=25438 dbname=bladepro_test user=bladepro_test"); err != nil {
		t.Fatal(err)
	}
}
