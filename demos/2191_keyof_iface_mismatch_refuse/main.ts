interface O { a: number; b: number; }
interface P { z: number; }
function main(): number {
  const o: O = { a: 10, b: 32 };
  return (o as P)["z"];
}
console.log(main())
