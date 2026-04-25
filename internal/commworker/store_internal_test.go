package commworker

import (
	"context"
	"errors"
	"testing"

	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	commStoreTestAgentID = "0xagent"
	commStoreTestDomain  = "example.com"
)

type commStoreTestDB struct {
	db       *ttmocks.MockExtendedDB
	qEmail   *ttmocks.MockQuery
	qPhone   *ttmocks.MockQuery
	qIdent   *ttmocks.MockQuery
	qChannel *ttmocks.MockQuery
	qPrefs   *ttmocks.MockQuery
	qAct     *ttmocks.MockQuery
	qQueue   *ttmocks.MockQuery
	qMailbox *ttmocks.MockQuery
	qEvent   *ttmocks.MockQuery
	qDomain  *ttmocks.MockQuery
	qInst    *ttmocks.MockQuery
}

func newCommStoreTestDB() commStoreTestDB {
	db := ttmocks.NewMockExtendedDB()
	qEmail := new(ttmocks.MockQuery)
	qPhone := new(ttmocks.MockQuery)
	qIdent := new(ttmocks.MockQuery)
	qChannel := new(ttmocks.MockQuery)
	qPrefs := new(ttmocks.MockQuery)
	qAct := new(ttmocks.MockQuery)
	qQueue := new(ttmocks.MockQuery)
	qMailbox := new(ttmocks.MockQuery)
	qEvent := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qInst := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(qEmail).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(qPhone).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(qIdent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentChannel")).Return(qChannel).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(qPrefs).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommActivity")).Return(qAct).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulAgentCommQueue")).Return(qQueue).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxMessage")).Return(qMailbox).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMailboxEvent")).Return(qEvent).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()

	for _, q := range []*ttmocks.MockQuery{qEmail, qPhone, qIdent, qChannel, qPrefs, qAct, qQueue, qMailbox, qEvent, qDomain, qInst} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("OrderBy", mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}
	qMailbox.On("Update", "Status", "UpdatedAt").Return(nil).Maybe()

	return commStoreTestDB{
		db:       db,
		qEmail:   qEmail,
		qPhone:   qPhone,
		qIdent:   qIdent,
		qChannel: qChannel,
		qPrefs:   qPrefs,
		qAct:     qAct,
		qQueue:   qQueue,
		qMailbox: qMailbox,
		qEvent:   qEvent,
		qDomain:  qDomain,
		qInst:    qInst,
	}
}

func TestNewDynamoStore(t *testing.T) {
	if got := newDynamoStore(nil); got == nil || got.db != nil {
		t.Fatalf("expected empty store wrapper, got %#v", got)
	}

	tdb := newCommStoreTestDB()
	got := newDynamoStore(store.New(tdb.db))
	if got == nil || got.db == nil {
		t.Fatalf("expected db-backed store, got %#v", got)
	}
}

func TestDynamoStoreLookupsAndGets(t *testing.T) {
	ctx := context.Background()

	var nilStore *dynamoStore
	if _, _, err := nilStore.LookupAgentByEmail(ctx, "user@example.com"); err == nil {
		t.Fatalf("expected store not initialized error")
	}

	runLookupAgentByEmailTest(t, ctx)
	runLookupAgentByPhoneTest(t, ctx)
	runGetSoulAgentIdentityTest(t, ctx)
	runGetSoulAgentChannelTest(t, ctx)
	runGetSoulAgentContactPreferencesTest(t, ctx)
	runGetDomainAndInstanceTest(t, ctx)
}

func runLookupAgentByEmailTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("lookup email success lowercases agent id", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qEmail.On("First", mock.AnythingOfType("*models.SoulEmailAgentIndex")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulEmailAgentIndex](t, args, 0)
			dest.AgentID = " 0xABCD "
		}).Once()

		st := &dynamoStore{db: tdb.db}
		agentID, ok, err := st.LookupAgentByEmail(ctx, " User@example.com ")
		if err != nil || !ok || agentID != "0xabcd" {
			t.Fatalf("unexpected lookup result: agentID=%q ok=%v err=%v", agentID, ok, err)
		}
	})
}

func runLookupAgentByPhoneTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("lookup phone not found", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qPhone.On("First", mock.AnythingOfType("*models.SoulPhoneAgentIndex")).Return(theoryErrors.ErrItemNotFound).Once()

		st := &dynamoStore{db: tdb.db}
		agentID, ok, err := st.LookupAgentByPhone(ctx, "+15551234567")
		if err != nil || ok || agentID != "" {
			t.Fatalf("unexpected lookup result: agentID=%q ok=%v err=%v", agentID, ok, err)
		}
	})
}

func runGetSoulAgentIdentityTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("identity validation and success", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qIdent.On("First", mock.AnythingOfType("*models.SoulAgentIdentity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentIdentity](t, args, 0)
			dest.AgentID = commStoreTestAgentID
		}).Once()

		st := &dynamoStore{db: tdb.db}
		if _, _, err := st.GetSoulAgentIdentity(ctx, " "); err == nil {
			t.Fatalf("expected validation error for empty agent id")
		}
		item, ok, err := st.GetSoulAgentIdentity(ctx, " 0xAGENT ")
		if err != nil || !ok || item == nil || item.AgentID != commStoreTestAgentID {
			t.Fatalf("unexpected identity result: item=%#v ok=%v err=%v", item, ok, err)
		}
	})
}

func runGetSoulAgentChannelTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("channel validation and not found", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qChannel.On("First", mock.AnythingOfType("*models.SoulAgentChannel")).Return(theoryErrors.ErrItemNotFound).Once()

		st := &dynamoStore{db: tdb.db}
		if _, _, err := st.GetSoulAgentChannel(ctx, commStoreTestAgentID, " "); err == nil {
			t.Fatalf("expected validation error for empty channel type")
		}
		item, ok, err := st.GetSoulAgentChannel(ctx, " 0xAGENT ", " EMAIL ")
		if err != nil || ok || item != nil {
			t.Fatalf("unexpected channel result: item=%#v ok=%v err=%v", item, ok, err)
		}
	})
}

func runGetSoulAgentContactPreferencesTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("contact preferences success", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qPrefs.On("First", mock.AnythingOfType("*models.SoulAgentContactPreferences")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulAgentContactPreferences](t, args, 0)
			dest.AgentID = commStoreTestAgentID
		}).Once()

		st := &dynamoStore{db: tdb.db}
		item, ok, err := st.GetSoulAgentContactPreferences(ctx, commStoreTestAgentID)
		if err != nil || !ok || item == nil || item.AgentID != commStoreTestAgentID {
			t.Fatalf("unexpected prefs result: item=%#v ok=%v err=%v", item, ok, err)
		}
	})
}

func runGetDomainAndInstanceTest(t *testing.T, ctx context.Context) {
	t.Helper()

	t.Run("domain success and instance error", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
			dest.Domain = commStoreTestDomain
		}).Once()
		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(errors.New("boom")).Once()

		st := &dynamoStore{db: tdb.db}
		domain, ok, err := st.GetDomain(ctx, " Example.com ")
		if err != nil || !ok || domain == nil || domain.Domain != commStoreTestDomain {
			t.Fatalf("unexpected domain result: domain=%#v ok=%v err=%v", domain, ok, err)
		}
		if _, _, err := st.GetInstance(ctx, "slug"); err == nil {
			t.Fatalf("expected instance error")
		}
	})
}

func TestDynamoStoreListAndPut(t *testing.T) {
	ctx := context.Background()

	var nilStore *dynamoStore
	if _, err := nilStore.ListRecentCommActivities(ctx, commStoreTestAgentID, 10); err == nil {
		t.Fatalf("expected store not initialized error")
	}

	t.Run("list activities clamps default limit", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qAct.On("Limit", 250).Return(tdb.qAct).Once()
		tdb.qAct.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{{AgentID: commStoreTestAgentID}}
		}).Once()

		st := &dynamoStore{db: tdb.db}
		items, err := st.ListRecentCommActivities(ctx, commStoreTestAgentID, -1)
		if err != nil || len(items) != 1 {
			t.Fatalf("unexpected activities result: items=%#v err=%v", items, err)
		}
	})

	t.Run("list activities clamps maximum limit", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		tdb.qAct.On("Limit", 1000).Return(tdb.qAct).Once()
		tdb.qAct.On("All", mock.AnythingOfType("*[]*models.SoulAgentCommActivity")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*[]*models.SoulAgentCommActivity](t, args, 0)
			*dest = []*models.SoulAgentCommActivity{}
		}).Once()

		st := &dynamoStore{db: tdb.db}
		items, err := st.ListRecentCommActivities(ctx, commStoreTestAgentID, 5000)
		if err != nil || len(items) != 0 {
			t.Fatalf("unexpected activities result: items=%#v err=%v", items, err)
		}
	})

	t.Run("put helpers validate nil and write", func(t *testing.T) {
		tdb := newCommStoreTestDB()
		st := &dynamoStore{db: tdb.db}

		if err := st.PutCommActivity(ctx, nil); err == nil {
			t.Fatalf("expected error for nil activity")
		}
		if err := st.PutCommQueue(ctx, nil); err == nil {
			t.Fatalf("expected error for nil queue item")
		}
		if err := st.PutMailboxMessage(ctx, nil); err == nil {
			t.Fatalf("expected error for nil mailbox message")
		}
		if err := st.PutMailboxEvent(ctx, nil); err == nil {
			t.Fatalf("expected error for nil mailbox event")
		}
		if err := st.UpdateMailboxMessageStatus(ctx, nil); err == nil {
			t.Fatalf("expected error for nil mailbox update")
		}
		if err := st.PutCommActivity(ctx, &models.SoulAgentCommActivity{AgentID: commStoreTestAgentID}); err != nil {
			t.Fatalf("PutCommActivity: %v", err)
		}
		if err := st.PutCommQueue(ctx, &models.SoulAgentCommQueue{AgentID: commStoreTestAgentID}); err != nil {
			t.Fatalf("PutCommQueue: %v", err)
		}
		if err := st.PutMailboxMessage(ctx, &models.SoulCommMailboxMessage{AgentID: commStoreTestAgentID}); err != nil {
			t.Fatalf("PutMailboxMessage: %v", err)
		}
		if err := st.PutMailboxEvent(ctx, &models.SoulCommMailboxEvent{AgentID: commStoreTestAgentID}); err != nil {
			t.Fatalf("PutMailboxEvent: %v", err)
		}
		if err := st.UpdateMailboxMessageStatus(ctx, &models.SoulCommMailboxMessage{AgentID: commStoreTestAgentID}); err != nil {
			t.Fatalf("UpdateMailboxMessageStatus: %v", err)
		}
	})
}
