class C {
  a: number = 0;
  b: number = 0;
  constructor(k: number) { this.a = this.b + k; this.b = 5; }
}
function main(): i32 {
  const c = new C(7);
  console.log(c.a);
  console.log(c.b);
  return 0;
}
