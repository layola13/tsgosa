class C {
  n: number = 10;
  constructor(k: number) { this.n -= k; this.n *= 2; }
}
function main(): i32 {
  const c = new C(3);
  console.log(c.n);
  return 0;
}
