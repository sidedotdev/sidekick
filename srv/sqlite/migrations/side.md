Migration start with an integer digit, eg 1 or 2. Both up and down migrations
should be added, as *.up.sql and *.down.sql. For example:

    - 1_create_subflows_table.up.sql
    - 1_create_subflows_table.down.sql

Table and column names should be lowercase snake_case.