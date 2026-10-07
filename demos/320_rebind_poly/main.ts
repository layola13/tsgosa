class B {
  bv: number = 0;
  get(): number { return this.bv + 1; }
}
class D extends B {
  get(): number { return this.bv + 100; }
}
function main(): number {
  let b: B = new B();
  b.bv = 1;
  const r0 = b.get();
  b = new D();
  b.bv = 2;
  const r1 = b.get();
  console.log(r0);
  console.log(r1);
  return r0 + r1 * 1000;
}
console.log(main());
