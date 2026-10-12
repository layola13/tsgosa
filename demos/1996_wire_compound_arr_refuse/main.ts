class C {
  a: number[] = [1];
  constructor(k: number) { this.a += k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.a);
  return 0;
}
