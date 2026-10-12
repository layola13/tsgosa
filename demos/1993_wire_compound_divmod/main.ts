class C {
  n: number = 20;
  constructor(k: number) { this.n /= k; this.n %= 3; }
}
function main(): i32 {
  const c = new C(4);
  console.log(c.n);
  return 0;
}
