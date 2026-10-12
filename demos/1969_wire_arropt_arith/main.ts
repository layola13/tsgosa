class C {
  n: number = 0;
  constructor(a: number[]|null) { this.n = (a?.[0] ?? 0) + 1; }
}
function main(): i32 {
  const c = new C([8, 9]);
  console.log(c.n);
  let e: number[]|null = null;
  const d = new C(e);
  console.log(d.n);
  return 0;
}
