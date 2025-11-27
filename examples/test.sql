-- Test SQL file for GoRAGlite

CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_users_email ON users(email);

CREATE TABLE posts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    content TEXT,
    published BOOLEAN DEFAULT FALSE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

SELECT u.name, COUNT(p.id) as post_count
FROM users u
LEFT JOIN posts p ON u.id = p.user_id
WHERE u.created_at > '2024-01-01'
GROUP BY u.id
HAVING COUNT(p.id) > 5
ORDER BY post_count DESC
LIMIT 10;

INSERT INTO users (name, email) VALUES ('John', 'john@example.com');

UPDATE posts SET published = TRUE WHERE user_id = 1 AND title LIKE '%draft%';

DELETE FROM posts WHERE published = FALSE AND created_at < '2023-01-01';

WITH active_users AS (
    SELECT user_id, COUNT(*) as cnt
    FROM posts
    WHERE published = TRUE
    GROUP BY user_id
)
SELECT u.name, au.cnt
FROM users u
INNER JOIN active_users au ON u.id = au.user_id
WHERE au.cnt > 10;
