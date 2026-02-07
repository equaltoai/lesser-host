package trust

import (
	"reflect"

	"github.com/stretchr/testify/mock"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"
)

type modelQueryPair struct {
	model any
	query *ttmocks.MockQuery
}

func newTestDBWithModelQueries(pairs ...modelQueryPair) *ttmocks.MockExtendedDB {
	db := ttmocks.NewMockExtendedDB()
	db.On("WithContext", mock.Anything).Return(db).Maybe()

	queries := make([]*ttmocks.MockQuery, 0, len(pairs))
	for _, pair := range pairs {
		if pair.model == nil || pair.query == nil {
			continue
		}

		db.On("Model", mock.AnythingOfType(typeString(pair.model))).Return(pair.query).Maybe()
		queries = append(queries, pair.query)
	}

	addStandardMockQueryStubs(queries...)

	return db
}

func addStandardMockQueryStubs(queries ...*ttmocks.MockQuery) {
	for _, query := range queries {
		query.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(query).Maybe()
		query.On("Filter", mock.Anything, mock.Anything, mock.Anything).Return(query).Maybe()
		query.On("Index", mock.Anything).Return(query).Maybe()
		query.On("Limit", mock.Anything).Return(query).Maybe()
		query.On("IfExists").Return(query).Maybe()
		query.On("IfNotExists").Return(query).Maybe()
		query.On("ConsistentRead").Return(query).Maybe()
	}
}

func typeString(v any) string {
	t := reflect.TypeOf(v)
	if t == nil {
		return ""
	}
	return t.String()
}
