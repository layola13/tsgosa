function getN(): number { return 1; }
function main(): i32 {
  const f: boolean = true;
  const x: boolean = f == getN();
  console.log(x);
  return 0;
}
