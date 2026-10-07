class B {
  bv: number = 0;
  constructor(n: number) { this.bv = n; }
  get(): number { return this.bv + 1; }
}
class D extends B {
  dv: number = 0;
  constructor(n: number, m: number) {
    super(n);
    this.dv = m;
  }
  get(): number { return this.bv + this.dv + 100; }
}
function main(): number {
  const b: B = new D(5, 0);
  const r1 = b.get();
  const d = new D(1, 2);
  const r2 = d.get();
  console.log(r1);
  console.log(r2);
  return r1 + r2 * 1000;
}
console.log(main());
