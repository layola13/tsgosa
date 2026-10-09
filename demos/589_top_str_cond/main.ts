const hi = "hi";
const empty = "";

if (hi) {
  console.log(1);
} else {
  console.log(0);
}
if (empty) {
  console.log(1);
} else {
  console.log(0);
}
if (!empty) {
  console.log(2);
}
let n = 0;
while (hi) {
  console.log(n + 3);
  break;
}
