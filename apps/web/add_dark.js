const fs = require('fs');
let code = fs.readFileSync('src/app/page.tsx', 'utf8');

const replacements = [
  { regex: /bg-gray-50(?! dark:)/g, repl: 'bg-gray-50 dark:bg-gray-950' },
  { regex: /bg-white(?! dark:)/g, repl: 'bg-white dark:bg-gray-900' },
  { regex: /text-gray-900(?! dark:)/g, repl: 'text-gray-900 dark:text-gray-100' },
  { regex: /text-gray-800(?! dark:)/g, repl: 'text-gray-800 dark:text-gray-200' },
  { regex: /text-gray-700(?! dark:)/g, repl: 'text-gray-700 dark:text-gray-300' },
  { regex: /text-gray-600(?! dark:)/g, repl: 'text-gray-600 dark:text-gray-400' },
  { regex: /text-gray-500(?! dark:)/g, repl: 'text-gray-500 dark:text-gray-400' },
  { regex: /border-gray-200(?! dark:)/g, repl: 'border-gray-200 dark:border-gray-800' },
  { regex: /border-gray-300(?! dark:)/g, repl: 'border-gray-300 dark:border-gray-700' },
  { regex: /border-gray-100(?! dark:)/g, repl: 'border-gray-100 dark:border-gray-800' }
];

replacements.forEach(({regex, repl}) => {
  code = code.replace(regex, repl);
});

fs.writeFileSync('src/app/page.tsx', code);
console.log('Done replacing colors');
