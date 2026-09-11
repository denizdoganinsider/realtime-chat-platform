-- notification-service owns its own schema (database-per-service), exactly as
-- chat-service does. user_id columns deliberately have NO foreign key to
-- chat_gateway_db.users: the id arrived through the gateway, which validated the
-- token that carried it, and MySQL cannot enforce integrity across schemas owned
-- by different deployables.
--
-- Self-contained (CREATE DATABASE + USE + IF NOT EXISTS) so it can be applied to
-- a live volume as well as run as a docker-entrypoint-initdb.d script:
--   docker exec -i chat_gateway_mysql mysql -uroot -proot < db/notification_schema.sql

CREATE DATABASE IF NOT EXISTS notification_service_db;

USE notification_service_db;

-- One endpoint per user, replaced by PUT. The secret signs every delivery body
-- (HMAC-SHA256); it is stored in the clear because signing needs the value, not
-- a hash of it. That is the standard webhook trade-off - the secret protects the
-- receiver from forged calls, it does not protect this table.
CREATE TABLE IF NOT EXISTS webhooks (
    user_id BIGINT PRIMARY KEY,
    url VARCHAR(2048) NOT NULL,
    secret VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

-- Which rooms a user wants to hear about while offline. Rooms are ad hoc (any
-- name a client asks for), so there is no rooms table to reference.
CREATE TABLE IF NOT EXISTS subscriptions (
    user_id BIGINT NOT NULL,
    room VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, room),
    INDEX idx_subscriptions_room (room)
);

-- Delivery log: one row per (event, recipient), updated as attempts happen. This
-- is what GET /deliveries reads and what the verification watches; it is not a
-- queue - the queue is in memory, and a row here records what the queue did.
CREATE TABLE IF NOT EXISTS deliveries (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    event_id CHAR(32) NOT NULL,
    user_id BIGINT NOT NULL,
    room VARCHAR(64) NOT NULL,
    url VARCHAR(2048) NOT NULL,
    status ENUM('pending', 'delivered', 'failed') NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    last_status INT NULL,
    last_error VARCHAR(512) NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    INDEX idx_deliveries_user_created (user_id, created_at),
    UNIQUE KEY uq_deliveries_event_user (event_id, user_id)
);
