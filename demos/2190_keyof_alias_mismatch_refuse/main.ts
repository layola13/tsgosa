interface O { a: number; b: number; }
interface P { z: number; }
type P2 = P;
function main(): number {
  const o: O = { a: 10, b: 32 };
  return (o as P2)["z"];
}
console.log(main())
