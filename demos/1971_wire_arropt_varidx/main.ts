class C {
  n: number = 0;
  constructor(a: number[]|null, i: number) { this.n = a?.[i] ?? -1; }
}
function main(): i32 {
  const c = new C([8, 9], 1);
  console.log(c.n);
  return 0;
}
