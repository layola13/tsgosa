interface O { a: number; b: number; }
type O2 = O;
function main(): number {
  const o: O = { a: 10, b: 32 };
  return (o satisfies O2)["a"];
}
console.log(main())
