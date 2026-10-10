interface W { tag: string; n: i32; }
function main(): i32 {
  const w: W = { tag: "x", n: 9 };
  console.log(w.tag + w.n);
  return 0;
}
