class B { v: number = 1; get(): number { return this.v; } }
class C extends B {
  run(): number { const f = () => this.get(); return f(); }
  sum(k: number): number { const g = (m: number) => this.v + m + k; return g(10); }
  nest(): number { const f = () => { const g = () => this.v; return g(); }; return f(); }
}
function main(): number {
  const c = new C(); c.v = 5;
  const d = new C(); d.v = 7;
  const r1 = c.run();
  const r2 = d.run();
  const r3 = c.sum(100);
  const r4 = c.nest();
  console.log(r1);
  console.log(r2);
  console.log(r3);
  console.log(r4);
  return r1 + r2 * 10 + r3 * 100 + r4 * 1000;
}
console.log(main());
