import http from 'k6/http';
import { check, sleep } from 'k6';

// Environment variables:
// MODE: "contention" (default) or "healthy"
// TARGET_URL: Checkout service URL (default http://localhost:8080)
// FAKEDB_URL: Optional direct URL to fakedb to configure mode during setup (default http://localhost:8081)
const MODE = __ENV.MODE || 'contention';
const TARGET_URL = __ENV.TARGET_URL || 'http://localhost:8080';
const FAKEDB_URL = __ENV.FAKEDB_URL || 'http://localhost:8081';

export const options = {
  scenarios: {
    surge: {
      executor: 'ramping-arrival-rate',
      startRate: 20,
      timeUnit: '1s',
      preAllocatedVUs: 50,
      maxVUs: 200,
      stages: [
        // 1. Baseline: 20 RPS (healthy on 3 pods, CPU < 50% target)
        { target: 20, duration: '1m' },
        // 2. Surge ramp: ramp to 120 RPS (drives CPU > 50% target to trigger HPA scale-up)
        { target: 120, duration: '1m' },
        // 3. Sustained surge: hold 120 RPS while checkout scales from 3 to 10 pods
        { target: 120, duration: '2m' },
        // 4. Cool down: ramp down to 20 RPS
        { target: 20, duration: '1m' },
      ],
    },
  },
  thresholds: {
    // In contention mode, 503s or high latency may occur; don't abort the test run
    checks: ['rate>0'],
  },
};

export function setup() {
  const dbMode = MODE === 'healthy' ? 'normal' : 'contention';
  console.log(`Starting surge test with MODE=${MODE} (fakedb mode=${dbMode})`);
  console.log(`Target checkout URL: ${TARGET_URL}`);

  // Attempt to set fakedb mode if reachable
  try {
    const res = http.post(`${FAKEDB_URL}/mode?mode=${dbMode}`, null, { timeout: '2s' });
    if (res.status === 200) {
      console.log(`Successfully configured fakedb mode to ${dbMode}`);
    }
  } catch (e) {
    console.log(`Note: could not reach ${FAKEDB_URL} to auto-set mode (ensure fakedb pod is configured with MODE=${dbMode})`);
  }

  return { mode: MODE, dbMode: dbMode };
}

export default function () {
  const res = http.post(`${TARGET_URL}/`, null, {
    headers: { 'Content-Type': 'application/json' },
  });

  check(res, {
    'status is valid': (r) => r.status === 200 || r.status === 503 || r.status === 502,
  });
}
