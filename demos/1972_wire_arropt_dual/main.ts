class C {
  a: number = 0;
  b: number = 0;
  constructor(q: number[]|null) { this.a = q?.[0] ?? -1; this.b = q?.[1] ?? -2; }
}
function main(): i32 {
  const c = new C([8, 9]);
  console.log(c.a);
  console.log(c.b);
  let e: number[]|null = null;
  const d = new C(e);
  console.log(d.a);
  console.log(d.b);
  return 0;
}
