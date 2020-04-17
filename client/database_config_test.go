package client

import (
	"encoding/json"
	"testing"
)

// Using table driven tests as described here: https://github.com/golang/go/wiki/TableDrivenTests#parallel-testing
func TestDatabaseConfigConnectionString(t *testing.T) {
	t.Parallel() // marks table driven test execution function as capable of running in parallel with other tests

	tests := []struct {
		name   string
		config DatabaseConfig
		want   string
	}{
		{
			name: "Simple",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
			},
			want: "host=localhost port=5432 user=simple password=database_config dbname=simple_database_config sslmode=disable",
		},
		{
			name: "SSLMode",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"sslmode": "prefer",
				},
			},
			want: "host=localhost port=5432 user=simple password=database_config dbname=simple_database_config sslmode=prefer",
		},
		{
			name: "Complex",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"connect_timeout": "10",
					"sslmode":         "verify-full",
					"sslcert":         "/app/certs/pg.pem",
					"sslkey":          "/app/certs/pg.key",
					"sslrootcert":     "/app/certs/pg_root.pem",
				},
			},
			want: "host=localhost port=5432 user=simple password=database_config dbname=simple_database_config connect_timeout=10 sslcert=/app/certs/pg.pem sslkey=/app/certs/pg.key sslmode=verify-full sslrootcert=/app/certs/pg_root.pem",
		},
	}

	for _, tt := range tests {
		tt := tt // NOTE: https://github.com/golang/go/wiki/CommonMistakes#using-goroutines-on-loop-iterator-variables
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // marks each test case as capable of running in parallel with each other

			if got := tt.config.ConnectionString(); got != tt.want {
				t.Errorf("invalid connection string, got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDatabaseConfigMarshal(t *testing.T) {
	t.Parallel() // marks table driven test execution function as capable of running in parallel with other tests

	tests := []struct {
		name   string
		config DatabaseConfig
		want   string
	}{
		{
			name: "Simple",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
			},
			want: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\"}",
		},
		{
			name: "SSLMode",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"sslmode": "prefer",
				},
			},
			want: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\",\"additionalParams\":{\"sslmode\":\"prefer\"}}",
		},
		{
			name: "Complex",
			config: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"connect_timeout": "10",
					"sslmode":         "verify-full",
					"sslcert":         "/app/certs/pg.pem",
					"sslkey":          "/app/certs/pg.key",
					"sslrootcert":     "/app/certs/pg_root.pem",
				},
			},
			want: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\",\"additionalParams\":{\"connect_timeout\":\"10\",\"sslcert\":\"/app/certs/pg.pem\",\"sslkey\":\"/app/certs/pg.key\",\"sslmode\":\"verify-full\",\"sslrootcert\":\"/app/certs/pg_root.pem\"}}",
		},
	}

	for _, tt := range tests {
		tt := tt // NOTE: https://github.com/golang/go/wiki/CommonMistakes#using-goroutines-on-loop-iterator-variables
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // marks each test case as capable of running in parallel with each other

			got, err := json.Marshal(tt.config)
			if err != nil {
				t.Fatalf("failed to marshal database config: %v", err)
			}

			if string(got) != tt.want {
				t.Errorf("invalid JSON string, got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDatabaseConfigUnmarshal(t *testing.T) {
	t.Parallel() // marks table driven test execution function as capable of running in parallel with other tests

	tests := []struct {
		name string
		want DatabaseConfig
		json string
	}{
		{
			name: "Simple",
			want: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
			},
			json: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\"}",
		},
		{
			name: "SSLMode",
			want: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"sslmode": "prefer",
				},
			},
			json: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\",\"additionalParams\":{\"sslmode\":\"prefer\"}}",
		},
		{
			name: "Complex",
			want: DatabaseConfig{
				Host:     "localhost",
				Port:     5432,
				Username: "simple",
				Password: "database_config",
				Database: "simple_database_config",
				AdditionalParams: map[string]string{
					"connect_timeout": "10",
					"sslmode":         "verify-full",
					"sslcert":         "/app/certs/pg.pem",
					"sslkey":          "/app/certs/pg.key",
					"sslrootcert":     "/app/certs/pg_root.pem",
				},
			},
			json: "{\"host\":\"localhost\",\"port\":5432,\"username\":\"simple\",\"password\":\"database_config\",\"database\":\"simple_database_config\",\"additionalParams\":{\"connect_timeout\":\"10\",\"sslcert\":\"/app/certs/pg.pem\",\"sslkey\":\"/app/certs/pg.key\",\"sslmode\":\"verify-full\",\"sslrootcert\":\"/app/certs/pg_root.pem\"}}",
		},
	}

	for _, tt := range tests {
		tt := tt // NOTE: https://github.com/golang/go/wiki/CommonMistakes#using-goroutines-on-loop-iterator-variables
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // marks each test case as capable of running in parallel with each other

			var got DatabaseConfig
			if err := json.Unmarshal([]byte(tt.json), &got); err != nil {
				t.Fatalf("failed to unmarshal database config: %v", err)
			}

			if got.Host != tt.want.Host {
				t.Errorf("invalid host, got %q, want %q", got.Host, tt.want.Host)
			}
			if got.Port != tt.want.Port {
				t.Errorf("invalid port, got %d, want %d", got.Port, tt.want.Port)
			}
			if got.Username != tt.want.Username {
				t.Errorf("invalid username, got %q, want %q", got.Username, tt.want.Username)
			}
			if got.Password != tt.want.Password {
				t.Errorf("invalid password, got %q, want %q", got.Password, tt.want.Password)
			}
			if got.Database != tt.want.Database {
				t.Errorf("invalid database, got %q, want %q", got.Database, tt.want.Database)
			}

			for k, v := range tt.want.AdditionalParams {
				g, ok := got.AdditionalParams[k]
				if !ok {
					t.Errorf("invalid additional parameter %q, got <nil>, want %q", k, v)
					continue
				}

				if g != v {
					t.Errorf("invalid additional parameter %q, got %q, want %q", k, g, v)
				}
			}
		})
	}
}
