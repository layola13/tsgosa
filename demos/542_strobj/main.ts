interface O { s: string; }
function main(): i32 {
  const o: O = { s: "hi" };
  console.log(o.s.length);
  return 0;
}
