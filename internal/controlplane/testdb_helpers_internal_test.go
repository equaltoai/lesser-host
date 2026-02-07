package controlplane

import (
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"
)

func newTestDBWithModelQueries(modelTypeNames ...string) (*ttmocks.MockExtendedDB, []*ttmocks.MockQuery) {
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	queries := make([]*ttmocks.MockQuery, 0, len(modelTypeNames))
	for _, typeName := range modelTypeNames {
		q := new(ttmocks.MockQuery)
		queries = append(queries, q)

		db.On("Model", mock.AnythingOfType(typeName)).Return(q).Maybe()
		addStandardMockQueryStubs(q)
	}

	return db, queries
}

func addStandardMockQueryStubs(q *ttmocks.MockQuery) {
	q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Filter", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
	q.On("Index", mock.Anything).Return(q).Maybe()
	q.On("Limit", mock.Anything).Return(q).Maybe()
	q.On("IfExists").Return(q).Maybe()
	q.On("IfNotExists").Return(q).Maybe()
	q.On("ConsistentRead").Return(q).Maybe()
	q.On("Create").Return(nil).Maybe()
	q.On("CreateOrUpdate").Return(nil).Maybe()
	q.On("Delete").Return(nil).Maybe()
	q.On("Update", mock.Anything).Return(nil).Maybe()
}

