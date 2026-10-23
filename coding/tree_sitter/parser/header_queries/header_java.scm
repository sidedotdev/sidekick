(
    (package_declaration) @header.package
    .
    (import_declaration) @header.imports
  (#select-adjacent! @header.package @header.imports)
) @header

(package_declaration) @header
(import_declaration) @header