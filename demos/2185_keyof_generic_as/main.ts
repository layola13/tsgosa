interface Box<T> { v: T; w: number; }
function main(): number {
  const b: Box<number> = { v: 3, w: 4 };
  return (b as Box<number>)["v"] + b["w"];
}
console.log(main())
