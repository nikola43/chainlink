package solana

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/errors"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/client"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/config"
	"github.com/smartcontractkit/chainlink-solana/pkg/solana/db"
	"github.com/smartcontractkit/chainlink/core/chains/solana/mocks"
	"github.com/smartcontractkit/chainlink/core/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const TestSolanaGenesisHashTemplate = `{"jsonrpc":"2.0","result":"%s","id":1}`

func TestSolanaChain_GetClient(t *testing.T) {
	checkOnce := map[string]struct{}{}
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := fmt.Sprintf(TestSolanaGenesisHashTemplate, client.MainnetGenesisHash) // mainnet genesis hash

		if !strings.Contains(r.URL.Path, "/mismatch") {
			// devnet gensis hash
			out = fmt.Sprintf(TestSolanaGenesisHashTemplate, client.DevnetGenesisHash)

			// clients with correct chainID should request chainID only once
			if _, exists := checkOnce[r.URL.Path]; exists {
				assert.NoError(t, errors.Errorf("rpc has been called once already for successful client '%s'", r.URL.Path))
			}
			checkOnce[r.URL.Path] = struct{}{}
		}

		_, err := w.Write([]byte(out))
		require.NoError(t, err)
	}))
	defer mockServer.Close()

	solORM := new(mocks.ORM)
	lggr := logger.TestLogger(t)
	testChain := chain{
		id:          "devnet",
		orm:         solORM,
		cfg:         config.NewConfig(db.ChainCfg{}, lggr),
		lggr:        logger.TestLogger(t),
		clientCache: map[string]cachedClient{},
	}

	// random nodes (happy path, all valid)
	solORM.On("NodesForChain", mock.Anything, mock.Anything, mock.Anything).Return([]db.Node{
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/1",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/2",
		},
	}, 2, nil).Once()
	_, err := testChain.getClient()
	assert.NoError(t, err)

	// random nodes (happy path, 1 valid + multiple invalid)
	solORM.On("NodesForChain", mock.Anything, mock.Anything, mock.Anything).Return([]db.Node{
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/1",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/1",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/2",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/3",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/4",
		},
	}, 2, nil).Once()
	_, err = testChain.getClient()
	assert.NoError(t, err)

	// empty nodes response
	solORM.On("NodesForChain", mock.Anything, mock.Anything, mock.Anything).Return([]db.Node{}, 0, nil).Once()
	_, err = testChain.getClient()
	assert.Error(t, err)

	// no valid nodes to select from
	solORM.On("NodesForChain", mock.Anything, mock.Anything, mock.Anything).Return([]db.Node{
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/1",
		},
		db.Node{
			SolanaChainID: "devnet",
			SolanaURL:     mockServer.URL + "/mismatch/2",
		},
	}, 2, nil).Once()
	_, err = testChain.getClient()
	assert.Error(t, err)
}

func TestSolanaChain_VerifiedClient(t *testing.T) {
	called := false
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := `{ "jsonrpc": "2.0", "result": 1234, "id": 1 }` // getSlot response

		body, err := ioutil.ReadAll(r.Body)
		require.NoError(t, err)

		// handle getGenesisHash request
		if strings.Contains(string(body), "getGenesisHash") {
			// should only be called once, chainID will be cached in chain
			if called {
				assert.NoError(t, errors.New("rpc has been called once already"))
			}
			// devnet gensis hash
			out = fmt.Sprintf(TestSolanaGenesisHashTemplate, client.DevnetGenesisHash)
		}
		_, err = w.Write([]byte(out))
		require.NoError(t, err)
		called = true
	}))
	defer mockServer.Close()

	lggr := logger.TestLogger(t)
	testChain := chain{
		cfg:         config.NewConfig(db.ChainCfg{}, lggr),
		lggr:        logger.TestLogger(t),
		clientCache: map[string]cachedClient{},
	}
	node := db.Node{SolanaURL: mockServer.URL}

	// happy path
	testChain.id = "devnet"
	_, err := testChain.verifiedClient(node)
	assert.NoError(t, err)

	// retrieve cached client and retrieve slot height
	c, err := testChain.verifiedClient(node)
	assert.NoError(t, err)
	slot, err := c.SlotHeight()
	assert.NoError(t, err)
	assert.Equal(t, uint64(1234), slot)

	// expect error from id mismatch (even if using a cached client)
	testChain.id = "incorrect"
	_, err = testChain.verifiedClient(node)
	assert.Error(t, err)
}

func TestSolanaChain_VerifiedClient_ParallelClients(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := fmt.Sprintf(TestSolanaGenesisHashTemplate, client.DevnetGenesisHash)
		_, err := w.Write([]byte(out))
		require.NoError(t, err)
	}))
	defer mockServer.Close()

	lggr := logger.TestLogger(t)
	testChain := chain{
		id:          "devnet",
		cfg:         config.NewConfig(db.ChainCfg{}, lggr),
		lggr:        logger.TestLogger(t),
		clientCache: map[string]cachedClient{},
	}
	node := db.Node{SolanaURL: mockServer.URL}

	var wg sync.WaitGroup
	wg.Add(2)

	var client0 client.ReaderWriter
	var client1 client.ReaderWriter
	var err0 error
	var err1 error

	// call verifiedClient in parallel
	go func() {
		client0, err0 = testChain.verifiedClient(node)
		assert.NoError(t, err0)
		wg.Done()
	}()
	go func() {
		client1, err1 = testChain.verifiedClient(node)
		assert.NoError(t, err1)
		wg.Done()
	}()

	wg.Wait()
	// check if pointers are all the same
	assert.Equal(t, testChain.clientCache[mockServer.URL].rw, client0)
	assert.Equal(t, testChain.clientCache[mockServer.URL].rw, client1)
}
