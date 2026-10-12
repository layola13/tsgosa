class C {
  a: number = 3;
  b: number = 4;
  n: number = 0;
  constructor() { this.n = this.a + this.b; }
}
function main(): i32 {
  const c = new C();
  console.log(c.n);
  return 0;
}
