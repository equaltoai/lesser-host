import '@apollo/client';

declare module '@apollo/client' {
	namespace ApolloClient {
		namespace DeclareDefaultOptions {
			interface WatchQuery {
				errorPolicy: 'all';
			}

			interface Query {
				errorPolicy: 'all';
			}

			interface Mutate {
				errorPolicy: 'all';
			}
		}
	}
}
