class Counter {
  count: i32 = 0;
  inc(): void {
    this.count = this.count + 1;
  }
}
function main(): i32 {
  const c = new Counter();
  c.inc();
  c.inc();
  console.log(c.count);
  return 0;
}
