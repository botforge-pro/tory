-- name: test-comments
SELECT 1 +
       -- comment
       2;

-- name: test-comments-after-semicolon
SELECT 1 +
       2; -- comment

-- name: test-arguments
SELECT name FROM users WHERE id = :id AND name ILIKE :q;

-- name: test.name-with-dots
SELECT 1;

-- name: test-sum
SELECT :x::int + :y::int;

-- name: test-do-block
do $$ begin
    create type test_dollar_color as enum ('red', 'green');
exception when duplicate_object then null; end $$;

-- name: test-do-block-multiple-statements
do $$ begin
    perform 1;
    perform 2;
end $$;

-- name: test-create-temp-table
create temporary table test_savepoint (n int) on commit drop;

-- name: test-insert
insert into test_savepoint (n) values (:n);

-- name: test-count
select count(*) from test_savepoint;

-- name: test-drop-data-migrations
drop table if exists data_migrations;

-- name: test-create-data-migrations-refusing-a-name
create table data_migrations (
    name text not null primary key check (name <> 'refused'),
    applied_at timestamptz not null default now()
);
