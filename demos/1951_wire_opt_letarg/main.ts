class C {
  n: number = 0;
  constructor(t: string|null) { this.n = t?.length ?? -1; }
}
function main(): i32 {
  let u: string|null = null;
  u = "ab";
  const c = new C(u);
  console.log(c.n);
  return 0;
}
