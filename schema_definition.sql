PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE `discord_name_override` (
    `id` INTEGER PRIMARY KEY AUTOINCREMENT,
    `discord_id` TEXT NOT NULL UNIQUE,
    `nickname`   TEXT NOT NULL,
    `row_created_at` DATETIME NOT NULL,
    `row_updated_at` DATETIME NOT NULL
);
CREATE TABLE `discord_name_differs` (
    `id` INTEGER PRIMARY KEY AUTOINCREMENT,
    `discord_id` TEXT NOT NULL,
    `row_created_at` DATETIME NOT NULL,
    `row_updated_at` DATETIME NOT NULL
);
CREATE TABLE `kol_blacklist` (
    `id` INTEGER PRIMARY KEY AUTOINCREMENT,
    `account_name` TEXT NOT NULL,
    `account_number` TEXT NOT NULL,
    `unique_ident`   TEXT NOT NULL UNIQUE,
    `reason` TEXT DEFAULT "",
    `row_created_at` DATETIME NOT NULL,
    `row_updated_at` DATETIME NOT NULL
);
DELETE FROM sqlite_sequence;
COMMIT;
