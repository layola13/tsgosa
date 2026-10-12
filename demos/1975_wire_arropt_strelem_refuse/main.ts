class C {
  n: number = 0;
  constructor(sa: string[]) { this.n = sa?.[0] ?? -1; }
}
function main(): i32 {
  const c = new C(["hi"]);
  console.log(c.n);
  return 0;
}
