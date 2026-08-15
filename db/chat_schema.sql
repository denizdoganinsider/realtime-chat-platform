-- chat-service owns its own schema (database-per-service). messages.user_id
-- deliberately has NO foreign key to chat_gateway_db.users: the two services are
-- independently deployable and MySQL cannot enforce integrity across schemas owned
-- by different deployables. The id is trusted because it came out of a signed JWT,
-- not because the database checked it.
--
-- This file is self-contained (CREATE DATABASE + USE + IF NOT EXISTS) so it can be
-- applied to a live volume as well as run as a docker-entrypoint-initdb.d script,
-- which only fires when the data directory is empty:
--   docker exec -i chat_gateway_mysql mysql -uroot -proot < db/chat_schema.sql

CREATE DATABASE IF NOT EXISTS chat_service_db;

USE chat_service_db;

-- created_at is TIMESTAMP(3) rather than TIMESTAMP because second-resolution ties
-- make LIMIT/OFFSET pagination non-deterministic: rows can repeat or vanish across
-- pages when several messages share a timestamp. Queries add ", id DESC" as a
-- tiebreak, which is free - InnoDB appends the primary key to every secondary
-- index, so idx_messages_room_created is physically (room_id, created_at, id).
CREATE TABLE IF NOT EXISTS messages (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    room_id VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    INDEX idx_messages_room_created (room_id, created_at)
);
