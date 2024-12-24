Migration start with a unique integer digit, incremented one higher than the
last one. Both up and down migrations should be added, as *.up.sql and
*.down.sql. For example:

    - 1_create_subflows_table.up.sql
    - 1_create_subflows_table.down.sql

Table and column names should be lowercase snake_case.

The error "duplicate migration file" indicates that an integer number for the
migration file prefix was reused and thus not correctly incremented. If you see
this, rename the file with the integer incremented properly.