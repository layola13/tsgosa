class C {
  n: number = 0;
  m: number = 10;
  constructor(k: number) { this.n = this.m + k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.n);
  return 0;
}
