interface O { a: number; b: number; }
class C {
  n: number = 5;
  count(): number {
    let t = 0;
    for (const k in this.n) { t += 1; }
    return t;
  }
}
function main(): number {
  const c = new C();
  return c.count();
}
console.log(main())
