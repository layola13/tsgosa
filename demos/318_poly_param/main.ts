class B {
  bv: number = 0;
  get(): number { return this.bv + 1; }
}
class D extends B {
  get(): number { return this.bv + 100; }
}
function use(p: B): number { return p.bv * 2 + 1; }
function main(): number {
  const d = new D();
  d.bv = 5;
  const r1 = use(d);
  const r2 = d.get();
  console.log(r1);
  console.log(r2);
  return r1 + r2 * 1000;
}
console.log(main());
