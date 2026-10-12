class C {
  n: number = 0;
  constructor(a: number[]|null) { this.n = a?.[1] ?? -1; }
}
function main(): i32 {
  let u: number[]|null = null;
  u = [7, 8, 9];
  const c = new C(u);
  console.log(c.n);
  return 0;
}
