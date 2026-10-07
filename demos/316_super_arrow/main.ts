class B { bv: number = 1; get(): number { return this.bv; } }
class D extends B {
  dv: number = 2;
  run(): number { const f = () => super.get(); return f(); }
}
function main(): number {
  const a = new D(); a.bv = 7;
  const b = new D(); b.bv = 40;
  const r1 = a.run();
  const r2 = b.run();
  console.log(r1);
  console.log(r2);
  return r1 + r2 * 100;
}
console.log(main());
