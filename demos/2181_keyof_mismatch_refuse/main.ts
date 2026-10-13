interface O { a: number; }
interface P { z: number; }
function main(): number {
  const o: O = { a: 7 };
  return (o as unknown as P)["z"];
}
console.log(main())
