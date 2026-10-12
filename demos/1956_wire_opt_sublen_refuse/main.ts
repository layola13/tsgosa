class C {
  n: number = 0;
  constructor(t: string|null) { this.n = t?.[0].length; }
}
function main(): i32 {
  const c = new C("hey");
  console.log(c.n);
  return 0;
}
