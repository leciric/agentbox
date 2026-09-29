import assert from 'node:assert/strict';
import { test } from 'node:test';
import { phoneName } from './phoneName.ts';

test('phoneName names the phone as its browser describes it', () => {
  const cases: [string, number, string][] = [
    ['Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148 Safari/604.1', 5, 'iPhone'],
    ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Version/18.0 Safari/605.1.15', 5, 'iPad'],
    ['Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/140.0.0.0 Mobile Safari/537.36', 5, 'Pixel 8'],
    ['Mozilla/5.0 (Linux; Android 13; SM-S911B Build/TP1A.220624.014) AppleWebKit/537.36 Chrome/140 Mobile Safari/537.36', 5, 'SM-S911B'],
    // Chrome's reduced user agent hides the model.
    ['Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 Chrome/140.0.0.0 Mobile Safari/537.36', 5, 'Android phone'],
    ['Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0', 0, 'Firefox'],
  ];
  for (const [ua, touch, want] of cases) assert.equal(phoneName(ua, touch), want, ua);
});
