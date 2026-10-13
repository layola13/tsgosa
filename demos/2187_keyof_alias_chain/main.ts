interface O { a: number; b: number; }
type O2 = O;
type O3 = O2;
function main(): number {
  const o: O = { a: 10, b: 32 };
  return (o as O3)["a"] + (o as O3)["b"];
}
console.log(main())
