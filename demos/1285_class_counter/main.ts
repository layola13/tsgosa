class Counter {
  n: i32 = 0;
  inc(): void { this.n = this.n + 1; }
  get(): i32 { return this.n; }
}
function main(): i32 {
  const c = new Counter();
  c.inc();
  c.inc();
  console.log(c.get());
  return 0;
}
