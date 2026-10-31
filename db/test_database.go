package db

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/lin9527ya/bladepro/config"
)

func resolveDatabaseDSN() (string, string, error) {
	if !testing.Testing() {
		return config.PostgresDSN()
	}
	dsn := strings.TrimSpace(os.Getenv("BLADEPRO_TEST_PG_DSN"))
	source := "环境变量 BLADEPRO_TEST_PG_DSN"
	if dsn == "" {
		var err error
		dsn, source, err = config.PostgresDSN()
		if err != nil {
			return "", source, err
		}
	}
	if err := guardTestDSN(dsn); err != nil {
		return "", source, err
	}
	return dsn, source, nil
}

func guardDatabaseForTests(dsn string) error {
	if !testing.Testing() {
		return nil
	}
	return guardTestDSN(dsn)
}

// Test binaries must never migrate or clean the configured application database,
// including through direct Open calls and keyword-form PostgreSQL DSNs.
func guardTestDSN(dsn string) error {
	c, err := pgx.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("无效的测试数据库配置；请设置 BLADEPRO_TEST_PG_DSN 指向隔离测试库")
	}
	if app, ok := config.ConfigPostgresDSN(); ok {
		a, err := pgx.ParseConfig(app)
		if err != nil {
			return fmt.Errorf("无法核验应用数据库配置；拒绝运行数据库测试")
		}
		if c.Database == a.Database && c.Port == a.Port && databaseHost(c.Host) == databaseHost(a.Host) {
			return fmt.Errorf("拒绝在应用数据库 %q 上运行测试；请使用独立测试库及账户", c.Database)
		}
	}
	n := strings.ToLower(c.Database)
	if n != "test" && !strings.HasPrefix(n, "test_") && !strings.HasSuffix(n, "_test") && !strings.Contains(n, "_test_") && !strings.HasPrefix(n, "acceptance_") && !strings.HasSuffix(n, "_acceptance") {
		return fmt.Errorf("拒绝在非测试库 %q 上运行测试；请设置 BLADEPRO_TEST_PG_DSN（如 bladepro_test）", c.Database)
	}
	return nil
}

func databaseHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "loopback"
	}
	return host
}
