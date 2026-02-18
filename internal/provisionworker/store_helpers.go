package provisionworker

import "fmt"

func (s *Server) requireStoreDB() error {
	if s == nil || s.store == nil || s.store.DB == nil {
		return fmt.Errorf("store not initialized")
	}
	return nil
}
