class B {
  bv: number = 0;
  get(): number { return this.bv + 1; }
}
class D extends B {
  get(): number { return this.bv + 100; }
}
function mk(): B { return new D(); }
function main(): number {
  const b = mk();
  b.bv = 5;
  const r1 = b.bv * 2 + 1;
  const d = new D();
  d.bv = 1;
  const r2 = d.get();
  console.log(r1);
  console.log(r2);
  return r1 + r2 * 1000;
}
console.log(main());
