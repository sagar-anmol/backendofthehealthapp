package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Token        string
	CreatedAt    time.Time
}

func InitDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite single-writer safe

	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		token TEXT UNIQUE NOT NULL,
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		memory_text TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		FOREIGN KEY(user_id) REFERENCES users(id)
	);
	CREATE INDEX IF NOT EXISTS idx_users_token ON users(token);
	CREATE INDEX IF NOT EXISTS idx_memories_user ON memories(user_id);
	`

	if _, err := conn.Exec(schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (d *DB) Close() error {
	return d.conn.Close()
}

func (d *DB) GetUserByUsername(username string) (*User, error) {
	row := d.conn.QueryRow("SELECT id, username, password_hash, token, created_at FROM users WHERE username = ?", username)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Token, &u.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (d *DB) GetUserByToken(token string) (*User, error) {
	row := d.conn.QueryRow("SELECT id, username, password_hash, token, created_at FROM users WHERE token = ?", token)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Token, &u.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (d *DB) CreateUser(username, passwordHash, token string) (*User, error) {
	now := time.Now().UTC()
	res, err := d.conn.Exec("INSERT INTO users (username, password_hash, token, created_at) VALUES (?, ?, ?, ?)",
		username, passwordHash, token, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{
		ID:           id,
		Username:     username,
		PasswordHash: passwordHash,
		Token:        token,
		CreatedAt:    now,
	}, nil
}

func (d *DB) SaveMemory(userID int64, memoryText string) error {
	now := time.Now().UTC()
	_, err := d.conn.Exec("INSERT INTO memories (user_id, memory_text, created_at) VALUES (?, ?, ?)",
		userID, memoryText, now)
	return err
}

func (d *DB) GetMemories(userID int64) ([]string, error) {
	rows, err := d.conn.Query("SELECT memory_text FROM memories WHERE user_id = ? ORDER BY id ASC LIMIT 20", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err == nil {
			memories = append(memories, m)
		}
	}
	return memories, nil
}
