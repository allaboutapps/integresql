package clienttest

import (
	"context"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/client"
	"github.com/allaboutapps/integresql/pgtestpool"
	"github.com/allaboutapps/integresql/server/api"
	"github.com/allaboutapps/integresql/server/router"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func initIsolatedManager(t *testing.T) (*httptest.Server, *pgtestpool.Manager, *client.Client) {
	managerConfig := pgtestpool.DefaultManagerConfigFromEnv()
	managerConfig.DatabasePrefix = fmt.Sprintf("test_%d", rand.Intn(1000))

	manager := pgtestpool.NewManager(managerConfig)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize manager: %v", err)
	}

	r := router.Init(&api.Server{M: manager})

	server := httptest.NewServer(r)

	client, err := client.NewClient(client.ClientConfig{
		BaseURL: fmt.Sprintf("%s/api", server.URL),
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	return server, manager, client
}

func disconnectManager(m *pgtestpool.Manager) {
	_ = m.Disconnect(context.Background(), true)
}
