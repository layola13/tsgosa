const S = ["a", "bb", "ccc"];
function main(): i32 {
  let t = "";
  for (const x of S) {
    t = t + x;
  }
  console.log(t);
  return 0;
}
