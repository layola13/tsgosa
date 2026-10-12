class C {
  n: number = 5;
  constructor() { this.n += 1; }
}
function main(): i32 {
  const c = new C();
  console.log(c.n);
  return 0;
}
