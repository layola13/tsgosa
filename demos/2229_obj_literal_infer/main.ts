interface Cfg {
  retries: number;
  timeout: number;
}

export function main(): i32 {
  const c = { retries: 3, timeout: 1000 };
  console.log(c.retries);
  console.log(c.timeout);
  const n = { inner: { v: 7 } };
  console.log(n.inner.v);
  const cfg: Cfg = { retries: 1, timeout: 2 };
  console.log(cfg.retries);
  const ks = Object.keys(c);
  console.log(ks.length);
  return 0;
}
