# DB_URL ?= $(DB_URL)
include .env
export

migrate-up:
	goose -dir sql/migrations postgres $(DB_URL) up

migrate-down:
	goose -dir sql/migrations postgres $(DB_URL) down