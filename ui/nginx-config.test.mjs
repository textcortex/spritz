import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import { uiPath } from './test-paths.mjs';

const nginxConfig = fs.readFileSync(uiPath('nginx.conf'), 'utf8');

test('nginx config disables caching for index and runtime config', () => {
  assert.match(nginxConfig, /location = \/index\.html \{[\s\S]*Cache-Control "no-store, max-age=0"/);
  assert.match(nginxConfig, /location = \/config\.js \{[\s\S]*Cache-Control "no-store, max-age=0"/);
});

test('nginx config keeps hashed js and css cacheable', () => {
  assert.match(nginxConfig, /location ~\* \\\.\(\?:js\|css\)\$/);
  assert.match(nginxConfig, /Cache-Control "public, max-age=31536000, immutable"/);
});
