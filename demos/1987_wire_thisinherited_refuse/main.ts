class B {
  v: number = 1;
}
class C extends B {
  n: number = 0;
  constructor(k: number) { super(); this.n = this.v + k; }
}
function main(): i32 {
  const c = new C(5);
  console.log(c.n);
  return 0;
}
