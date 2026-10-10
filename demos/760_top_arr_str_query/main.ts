const S = ["a", "bb", "ccc"];
function main(): i32 {
  console.log(S.indexOf("bb"));
  console.log(S.includes("bb"));
  console.log(S.includes("zz"));
  return 0;
}
