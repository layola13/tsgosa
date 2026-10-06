async function f(): Promise<number> {
  let s = 0;
  for await (const x of [1, 2, 3]) {
    s = s + x;
  }
  return s;
}
function main(): number {
  return f();
}
console.log(main());
