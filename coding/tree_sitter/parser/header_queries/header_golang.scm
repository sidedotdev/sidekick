(
    (package_clause) @header.package
    .
    (import_declaration) @header.imports
  (#select-adjacent! @header.package @header.imports)
) @header


(package_clause) @header
(import_declaration) @header