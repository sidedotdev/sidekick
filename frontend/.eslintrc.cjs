/* eslint-env node */
require('@rushstack/eslint-patch/modern-module-resolution')

module.exports = {
  root: true,
  ignorePatterns: ['dist'],
  'extends': [
    'plugin:vue/vue3-essential',
    'eslint:recommended',
    '@vue/eslint-config-typescript',
    '@vue/eslint-config-prettier/skip-formatting'
  ],
  parserOptions: {
    ecmaVersion: 'latest'
  },
  rules: {
    // Catches missing component imports in <template> blocks. Vue otherwise
    // resolves unknown tags via the global component registry at runtime,
    // logging only a console warning and silently rendering nothing.
    'vue/no-undef-components': ['error', {
      ignorePatterns: ['router-link', 'router-view']
    }],
    'vue/multi-word-component-names': ['error', {
      ignores: ['Tooltip']
    }]
  },
  overrides: [
    {
      files: ['nightwatch/**/*.js', 'nightwatch.conf.ts'],
      env: { node: true }
    }
  ]
}
