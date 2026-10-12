class C {
  n: number = 0;
  constructor(t: string|null) { this.n = t?.length ?? -1; }
}
function main(): i32 {
  const c = new C("hey");
  console.log(c.n);
  const d = new C(null);
  console.log(d.n);
  return 0;
}
