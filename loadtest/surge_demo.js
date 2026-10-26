import http from 'k6/http';
import { check } from 'k6';

const TARGET_URL = __ENV.TARGET_URL || 'http://localhost:8081';
const BASE_RPS = parseInt(__ENV.BASE_RPS || '60', 10);
const PEAK_RPS = parseInt(__ENV.PEAK_RPS || '490', 10);
const NO_REUSE = __ENV.NO_REUSE === '1';

export const options = {
  noConnectionReuse: NO_REUSE,
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(50)', 'p(90)', 'p(95)', 'p(99)'],
  scenarios: {
    surge_demo: {
      executor: 'ramping-arrival-rate',
      startRate: BASE_RPS,
      timeUnit: '1s',
      preAllocatedVUs: 100,
      maxVUs: 500,
      stages: [
        { target: BASE_RPS, duration: '60s' },
        { target: PEAK_RPS, duration: '10s' },
        { target: PEAK_RPS, duration: '150s' },
        { target: BASE_RPS, duration: '10s' },
        { target: BASE_RPS, duration: '120s' },
      ],
    },
  },
};

export default function () {
  const res = http.post(`${TARGET_URL}/`, null, {
    headers: { 'Content-Type': 'application/json' },
    timeout: '3s',
  });

  check(res, {
    'status is 200': (r) => r.status === 200,
  });
}
