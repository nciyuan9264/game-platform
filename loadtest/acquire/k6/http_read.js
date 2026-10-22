import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  vus: Number(__ENV.VUS || 10),
  duration: __ENV.DURATION || '5m',
};

const httpBase = (__ENV.ACQUIRE_HTTP_BASE || 'http://127.0.0.1:8083').replace(/\/$/, '');
const accessToken = __ENV.ACCESS_TOKEN || '';

function params() {
  if (!accessToken) return {};
  return {
    headers: {
      Authorization: `Bearer ${accessToken}`,
    },
  };
}

export default function () {
  const listResp = http.get(`${httpBase}/platform/rooms?gameType=acquire`, params());
  check(listResp, {
    'room list status is 200 or auth-blocked': (r) => r.status === 200 || r.status === 401,
  });

  const rankingResp = http.get(`${httpBase}/platform/rankings?gameType=acquire`);
  check(rankingResp, {
    'leaderboard status is 200': (r) => r.status === 200,
  });

  sleep(1);
}
