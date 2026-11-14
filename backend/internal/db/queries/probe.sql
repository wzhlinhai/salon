-- name: TransactionIsolation :one
SELECT current_setting('transaction_isolation') AS isolation;
