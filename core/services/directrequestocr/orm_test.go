package directrequestocr_test

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink/core/internal/testutils"
	"github.com/smartcontractkit/chainlink/core/internal/testutils/pgtest"
	"github.com/smartcontractkit/chainlink/core/logger"
	"github.com/smartcontractkit/chainlink/core/services/directrequestocr"
)

func setupORM(t *testing.T) directrequestocr.ORM {
	t.Helper()

	var (
		db       = pgtest.NewSqlxDB(t)
		lggr     = logger.TestLogger(t)
		contract = testutils.NewAddress()
		orm      = directrequestocr.NewORM(db, lggr, pgtest.NewQConfig(true), contract)
	)

	return orm
}

func newRequestID() directrequestocr.RequestID {
	return testutils.Random32Byte()
}

func createRequest(t *testing.T, orm directrequestocr.ORM) (directrequestocr.RequestID, common.Hash, time.Time) {
	ts := time.Now().Round(time.Second)
	id, hash := createRequestWithTimestamp(t, orm, ts)
	return id, hash, ts
}

func createRequestWithTimestamp(t *testing.T, orm directrequestocr.ORM, ts time.Time) (directrequestocr.RequestID, common.Hash) {
	id := newRequestID()
	txHash := testutils.NewAddress().Hash()
	err := orm.CreateRequest(id, ts, &txHash)
	require.NoError(t, err)
	return id, txHash
}

func TestORM_CreateRequestsAndFindByID(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	id1, txHash1, ts1 := createRequest(t, orm)
	id2, txHash2, ts2 := createRequest(t, orm)

	req1, err := orm.FindById(id1)
	require.NoError(t, err)
	require.Equal(t, id1, req1.RequestID)
	require.Equal(t, &txHash1, req1.RequestTxHash)
	require.Equal(t, ts1, req1.ReceivedAt)
	require.Equal(t, directrequestocr.IN_PROGRESS, req1.State)

	req2, err := orm.FindById(id2)
	require.NoError(t, err)
	require.Equal(t, id2, req2.RequestID)
	require.Equal(t, &txHash2, req2.RequestTxHash)
	require.Equal(t, ts2, req2.ReceivedAt)
	require.Equal(t, directrequestocr.IN_PROGRESS, req2.State)

	t.Run("missing ID", func(t *testing.T) {
		req, err := orm.FindById(newRequestID())
		require.Error(t, err)
		require.Nil(t, req)
	})

	t.Run("duplicated", func(t *testing.T) {
		err := orm.CreateRequest(id1, ts1, &txHash1)
		require.Error(t, err)
		err = orm.CreateRequest(id1, ts1, &txHash1)
		require.Error(t, err)
	})
}

func TestORM_SetResult(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	id, _, ts := createRequest(t, orm)

	rdts := time.Now().Round(time.Second)
	err := orm.SetResult(id, 123, []byte("result"), rdts)
	require.NoError(t, err)

	req, err := orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, id, req.RequestID)
	require.Equal(t, ts, req.ReceivedAt)
	require.NotNil(t, req.ResultReadyAt)
	require.Equal(t, rdts, *req.ResultReadyAt)
	require.Equal(t, directrequestocr.RESULT_READY, req.State)
	require.Equal(t, []byte("result"), req.Result)
	require.NotNil(t, req.RunID)
	require.Equal(t, int64(123), *req.RunID)
}

func TestORM_SetError(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	id, _, ts := createRequest(t, orm)

	rdts := time.Now().Round(time.Second)
	err := orm.SetError(id, 123, directrequestocr.USER_EXCEPTION, []byte("error"), rdts)
	require.NoError(t, err)

	req, err := orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, id, req.RequestID)
	require.Equal(t, ts, req.ReceivedAt)
	require.NotNil(t, req.ResultReadyAt)
	require.Equal(t, rdts, *req.ResultReadyAt)
	require.NotNil(t, req.ErrorType)
	require.Equal(t, directrequestocr.USER_EXCEPTION, *req.ErrorType)
	require.Equal(t, directrequestocr.RESULT_READY, req.State)
	require.Equal(t, []byte("error"), req.Error)
	require.NotNil(t, req.RunID)
	require.Equal(t, int64(123), *req.RunID)
}

func TestORM_SetTransmitted(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	id, _, _ := createRequest(t, orm)

	err := orm.SetTransmitted(id, []byte("result"), []byte("error"))
	require.NoError(t, err)

	req, err := orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, []byte("result"), req.TransmittedResult)
	require.Equal(t, []byte("error"), req.TransmittedError)
	require.Equal(t, directrequestocr.TRANSMITTED, req.State)
}

func TestORM_SetConfirmed(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	id, _, _ := createRequest(t, orm)

	err := orm.SetConfirmed(id)
	require.NoError(t, err)

	req, err := orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.CONFIRMED, req.State)
}

func TestORM_StateTransitions(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	now := time.Now()
	id, _ := createRequestWithTimestamp(t, orm, now)
	req, err := orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.IN_PROGRESS, req.State)

	err = orm.SetResult(id, 0, []byte{}, now)
	require.NoError(t, err)
	req, err = orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.RESULT_READY, req.State)

	_, err = orm.TimeoutExpiredResults(now.Add(time.Minute), 1)
	require.NoError(t, err)
	req, err = orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.TIMED_OUT, req.State)

	err = orm.SetTransmitted(id, nil, nil)
	require.Error(t, err)
	req, err = orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.TIMED_OUT, req.State)

	err = orm.SetConfirmed(id)
	require.NoError(t, err)
	req, err = orm.FindById(id)
	require.NoError(t, err)
	require.Equal(t, directrequestocr.CONFIRMED, req.State)
}

func TestORM_FindOldestEntriesByState(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	now := time.Now()
	id2, _ := createRequestWithTimestamp(t, orm, now.Add(2*time.Minute))
	createRequestWithTimestamp(t, orm, now.Add(3*time.Minute))
	id1, _ := createRequestWithTimestamp(t, orm, now.Add(1*time.Minute))

	t.Run("with limit", func(t *testing.T) {
		result, err := orm.FindOldestEntriesByState(directrequestocr.IN_PROGRESS, 2)
		require.NoError(t, err)
		require.Equal(t, 2, len(result), "incorrect results length")
		require.Equal(t, id1, result[0].RequestID, "incorrect results order")
		require.Equal(t, id2, result[1].RequestID, "incorrect results order")
	})

	t.Run("with no limit", func(t *testing.T) {
		result, err := orm.FindOldestEntriesByState(directrequestocr.IN_PROGRESS, 20)
		require.NoError(t, err)
		require.Equal(t, 3, len(result), "incorrect results length")
	})

	t.Run("no matching entries", func(t *testing.T) {
		result, err := orm.FindOldestEntriesByState(directrequestocr.RESULT_READY, 10)
		require.NoError(t, err)
		require.Equal(t, 0, len(result), "incorrect results length")
	})
}

func TestORM_TimeoutExpiredResults(t *testing.T) {
	t.Parallel()

	orm := setupORM(t)
	now := time.Now()
	var ids []directrequestocr.RequestID
	for offset := -50; offset <= -10; offset += 10 {
		id, _ := createRequestWithTimestamp(t, orm, now.Add(time.Duration(offset)*time.Minute))
		ids = append(ids, id)
	}
	// time out both IN_PROGRESS and RESULTS_READY states
	err := orm.SetResult(ids[0], 123, []byte("result"), now)
	require.NoError(t, err)

	results, err := orm.TimeoutExpiredResults(now.Add(-35*time.Minute), 1)
	require.NoError(t, err)
	require.Equal(t, 1, len(results), "not respecting limit")
	require.Equal(t, ids[0], results[0], "incorrect results order")

	results, err = orm.TimeoutExpiredResults(now.Add(-15*time.Minute), 10)
	require.NoError(t, err)
	require.Equal(t, 3, len(results), "incorrect results length")
	require.Equal(t, ids[1], results[0], "incorrect results order")
	require.Equal(t, ids[2], results[1], "incorrect results order")
	require.Equal(t, ids[3], results[2], "incorrect results order")

	results, err = orm.TimeoutExpiredResults(now.Add(-15*time.Minute), 10)
	require.NoError(t, err)
	require.Equal(t, 0, len(results), "not idempotent")

	for i := 0; i < 4; i++ {
		req, err := orm.FindById(ids[i])
		require.NoError(t, err)
		require.Equal(t, req.State, directrequestocr.TIMED_OUT, "incorrect state")
	}
}
