import http from 'k6/http';
import { check, sleep } from 'k6';

// k6 config targeting ~1,000,000 RPM (Requests Per Minute).
// 1,000,000 / 60 = 16,666 RPS.
// We use a constant arrival rate executor to ensure an exact throughput curve.
export const options = {
    scenarios: {
        target_1M_rpm: {
            executor: 'constant-arrival-rate',
            // Our target is 16,666 ops/s for 1M RPM.
            rate: 16666,
            timeUnit: '1s',
            // Duration of the test (e.g. 1 minute to prove 1M can be handled)
            duration: '1m',
            preAllocatedVUs: 500, // Pre-allocate enough VUs to maintain the rate
            maxVUs: 2000,         // Scale up if necessary
        },
    },
    thresholds: {
        // 99% of requests must complete below 50ms.
        http_req_duration: ['p(99)<50'],
        // 0% errors (every request succeeds)
        http_req_failed: ['rate<0.001'],
    },
};

// URL defaults to docker container name 'gateway' if run in compose network.
const BASE_URL = __ENV.GATEWAY_URL || 'http://localhost:8080';

export default function () {
    // We hit an endpoint that forwards to the echo server and uses rate limiting
    // The gateway_stress_test.yaml configures the rate limiter to memory, burst=500000 
    // to avoid rate-limiting ourselves during this benchmark.
    const url = `${BASE_URL}/api/v1/users/profile`;
    
    const params = {
        headers: {
            'x-user-id': `user-${Math.floor(Math.random() * 10000)}`, // Simulate different users
            'Authorization': 'Bearer placeholder',
        },
    };

    const res = http.get(url, params);

    check(res, {
        'status is 200': (r) => r.status === 200,
    });
}
