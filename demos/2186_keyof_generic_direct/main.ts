interface Box<T> { v: T; w: T; }
function main(): number {
  const b: Box<number> = { v: 3, w: 4 };
  return b["v"] + b["w"];
}
console.log(main())
