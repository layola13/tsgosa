class C {
  a: number = 1;
  b: number = 2;
  constructor(k: number) { this.a += this.b + k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.a);
  return 0;
}
