package store

import "database/sql"

func (s *Store) SettingGet(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

func (s *Store) SettingSet(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value, updated_at) VALUES (?,?,?)`,
		key, value, s.clock().Unix())
	return err
}
