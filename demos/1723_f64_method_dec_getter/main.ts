class F { rate: f64 = 5; tick(): void { this.rate--; } get(): f64 { return this.rate; } }
function main(): i32 {
  const f = new F();
  f.tick();
  f.tick();
  console.log(f.get());
  return 0;
}
