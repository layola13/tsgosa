interface P { name: string; }
function main(): i32 {
  const p: P = { name: "abc" };
  console.log(p.name[2]);
  return 0;
}
