function one(): number { return 1; }
function main(): i32 {
  const f: boolean = true;
  console.log(f == one());
  console.log(one() != f);
  return 0;
}
