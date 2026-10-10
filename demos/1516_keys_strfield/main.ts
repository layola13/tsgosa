interface R { tag: string; n: i32; }
function main(): i32 {
  const r: R = { tag: "ab", n: 5 };
  console.log(Object.keys(r).length);
  console.log(r.tag);
  return 0;
}
