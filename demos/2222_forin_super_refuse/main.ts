interface O { a: number; b: number; }
class C {
  o: O = { a: 1, b: 2 };
  count(): number {
    let t = 0;
    for (const k in super.o) { t += 1; }
    return t;
  }
}
function main(): number {
  const c = new C();
  return c.count();
}
console.log(main())
