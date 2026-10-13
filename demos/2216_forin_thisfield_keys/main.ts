interface O { a: number; b: number; }
class C {
  o: O = { a: 1, b: 2 };
  keys(): string {
    let s = "";
    for (const k in this.o) { s += k; }
    return s;
  }
}
function main(): string {
  const c = new C();
  return c.keys();
}
console.log(main())
