interface O { a: number; b: number; }
type O2 = O;
function main(): number {
  const o: O = { a: 10, b: 32 };
  const k = "b";
  return (o as O2)[k];
}
console.log(main())
