const A = [1, 2];
function main(): i32 {
  if (typeof A === "object") {
    console.log(1);
  }
  if (typeof A !== "undefined") {
    console.log(2);
  }
  return 0;
}
